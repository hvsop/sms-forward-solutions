package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	configPath := flag.String("config", ".env", "configuration file (environment variables override)")
	signupInvite := flag.Bool("create-signup-invite", false, "create a single-use 7-day signup invitation; requires -out")
	createShare := flag.String("create-share-user", "", "create an independent file-sharing user")
	createUser := flag.String("create-user", "", "create an invited user by alias")
	revokeUser := flag.String("revoke-user", "", "revoke and delete an account by ID; stop the server first")
	invitationOut := flag.String("out", "", "private invitation output file (never overwrite)")
	endpoint := flag.String("endpoint", "", "HTTPS endpoint included in invitation")
	initialize := flag.Bool("init", false, "create configuration with random secrets; never overwrite")
	flag.Parse()
	if *initialize {
		if err := initConfig(*configPath); err != nil {
			log.Fatal(err)
		}
		log.Print("configuration created with mode 0600; secrets are not printed")
		return
	}
	c, err := loadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if *signupInvite {
		if err := createSignupInvite(c, *invitationOut); err != nil {
			log.Fatal(err)
		}
		log.Print("signup invitation saved privately; valid for 7 days, single use")
		return
	}
	if *createShare != "" {
		if err := createShareOwner(c, *createShare, *endpoint, *invitationOut); err != nil {
			log.Fatal(err)
		}
		log.Print("private file-sharing invitation saved; no SMS access granted")
		return
	}
	if *createUser != "" {
		invitation, err := createAccount(c, *createUser, *endpoint, *invitationOut)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("account %s created; credentials saved privately to %s", invitation.AccountID, *invitationOut)
		return
	}
	lock, err := lockDataDirectory(c.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer lock.Close()
	if *revokeUser != "" {
		if err := revokeAccount(c, *revokeUser); err != nil {
			log.Fatal(err)
		}
		log.Print("account revoked and active inbox removed")
		return
	}
	apns, err := newAPNS(c)
	if err != nil {
		log.Fatal("APNs configuration: ", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	accounts, err := newAccounts(ctx, c, apns)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: c.Listen, Handler: accounts, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 8 << 10}
	serverDone := make(chan error, 1)
	go func() {
		if c.TLSCert != "" {
			serverDone <- server.ListenAndServeTLS(c.TLSCert, c.TLSKey)
		} else {
			serverDone <- server.ListenAndServe()
		}
	}()
	log.Printf("private-sms listening on %s; APNs configured=%t; retention=%s", c.Listen, apns != nil, c.Retention)
	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-serverDone:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Print("HTTP server stopped: ", err)
			serveErr = err
		}
		stop()
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	accounts.workers.Wait()
	if serveErr != nil {
		os.Exit(1)
	}
}

func worker(ctx context.Context, store *Store, apns *APNS) {
	pushTick := time.NewTicker(2 * time.Second)
	defer pushTick.Stop()
	purgeTick := time.NewTicker(time.Minute)
	defer purgeTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-purgeTick.C:
			if err := store.purge(now); err != nil {
				log.Print("retention cleanup failed")
			}
		case now := <-pushTick.C:
			if apns == nil {
				continue
			}
			store.pushMu.Lock()
			r, device, ok := store.due(now)
			if !ok {
				store.pushMu.Unlock()
				continue
			}
			invalid, err := apns.send(ctx, device, store.badge(time.Now()))
			if err != nil && ctx.Err() == nil {
				log.Print("push failed; retry retained; ", err)
			}
			if saveErr := store.pushResult(r.Message.ID, device, err, invalid, time.Now()); saveErr != nil {
				log.Print("push result could not be saved; notification may repeat")
			}
			store.pushMu.Unlock()
		}
	}
}
