import SwiftUI
import AuthenticationServices
import CoreImage.CIFilterBuiltins

private typealias ViewState<Value> = SwiftUI.State<Value>

struct AppleAccountView: View {
    @ObservedObject var model: InboxModel
    var linking = false
    @Environment(\.dismiss) private var dismiss
    @ViewState<String> private var invite = ""
    @ViewState<Bool> private var consent = false
    @ViewState<AppleChallenge?> private var challenge = nil
    @ViewState<String?> private var localError = nil
    @ViewState<Bool> private var preparing = false

    var body: some View {
        Form {
            Section {
                Label(linking ? "保留你的收件箱" : "短信，随身可见。", systemImage: "iphone.and.arrow.forward")
                    .font(.title2.bold()).padding(.vertical, 8)
                Text(linking ? "将当前账户绑定到 Apple 登录，保留短信、安卓配对和通知设置。以后换 iPhone 可以直接登录恢复。" : "用 Apple 账户登录，在 iPhone 上查看安卓手机收到的新短信。换机后使用同一个 Apple 账户即可恢复访问。")
            }
            if !linking {
                Section {
                    TextField("首次开通填写试用邀请码", text: $invite)
                        .textInputAutocapitalization(.characters).autocorrectionDisabled()
                } header: { Text("试用邀请码") } footer: { Text("已有 Apple 登录账户可留空。邀请码与安卓配对码不同，开通后不用保存。") }
            }
            Section {
                Text("短信通过 HTTPS 上传到 sms.example.com 并加密保存，默认保留 7 天。服务运营者可解密短信，当前不是端到端加密。苹果推送不包含短信正文。")
                    .font(.footnote).foregroundStyle(.secondary)
                Toggle("我已了解并同意上述短信处理方式", isOn: $consent)
                SignInWithAppleButton(linking ? .continue : .signIn) { request in
                    request.requestedScopes = []
                    request.nonce = challenge?.nonce
                    request.state = challenge?.challengeID
                } onCompletion: { result in
                    guard let current = challenge else { return }
                    challenge = nil
                    switch result {
                    case .success(let authorization):
                        guard let credential = authorization.credential as? ASAuthorizationAppleIDCredential else { return }
                        Task {
                            if await model.signInWithApple(credential, challenge: current, invite: invite, linking: linking) { dismiss() }
                            else { await prepare() }
                        }
                    case .failure(let error):
                        if (error as? ASAuthorizationError)?.code != .canceled { localError = "Apple 登录未完成，请重试。" }
                        Task { await prepare() }
                    }
                }
                .signInWithAppleButtonStyle(.black).frame(height: 48)
                .disabled(!consent || challenge == nil || model.isConnecting)
                if model.isConnecting || preparing { ProgressView(model.isConnecting ? "正在连接你的收件箱…" : "正在准备登录…") }
                if challenge == nil && !preparing && !model.isConnecting {
                    Button("重新准备登录") { Task { await prepare() } }
                }
            } footer: { Text("不读取 Apple 邮箱或姓名。首次开通后，在“安卓设备”中生成配对码。每个账户支持一台安卓发送端和一台 iPhone 推送接收端；重新登录会把接收权限切换到当前手机。") }
            if let error = localError ?? model.error { Section { Text(error).font(.footnote).foregroundStyle(.orange) } }
        }
        .navigationTitle(linking ? "绑定 Apple 账户" : "登录与开通")
        .navigationBarTitleDisplayMode(.inline)
        .task { await prepare() }
        .onDisappear { invite = ""; challenge = nil }
    }

    private func prepare() async {
        guard !preparing else { return }
        preparing = true; defer { preparing = false }
        do { challenge = try await APIClient.challenge(); localError = nil }
        catch { localError = (error as? ClientError)?.localizedDescription ?? "无法连接登录服务，请稍后重试。" }
    }
}

struct AndroidDevicesView: View {
    @ObservedObject var model: InboxModel
    @ViewState<Bool> private var confirmUnpair = false
    @ViewState<Bool> private var confirmReplace = false
    @ViewState<Bool> private var copied = false

    var body: some View {
        Form {
            Section("安卓发送端") {
                if let profile = model.profile {
                    Label(profile.androidPaired ? "已配对一台安卓手机" : "尚未配对", systemImage: profile.androidPaired ? "checkmark.circle.fill" : "smartphone")
                    if profile.androidPaired {
                        LabeledContent("最近连接服务器", value: profile.lastSeenLabel)
                        Text("此时间只表示手机联系过服务器，不代表短信权限或转发开关已开启。")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    Button(profile.androidPaired ? "更换安卓手机" : "添加安卓手机") {
                        if profile.androidPaired { confirmReplace = true } else { Task { await model.createPairing() } }
                    }.disabled(model.isManagingDevices)
                    if profile.androidPaired {
                        Button("解除安卓配对", role: .destructive) { confirmUnpair = true }.disabled(model.isManagingDevices)
                    }
                } else { ProgressView("正在查询设备…") }
            }
            if let pairing = model.pairingCode {
                Section("在安卓端扫码或输入配对码") {
                    TimelineView(.periodic(from: .now, by: 1)) { context in
                        if context.date < pairing.expiry {
                            VStack(spacing: 16) {
                                if let qr = qrCode(pairing.code) {
                                    Image(uiImage: qr).interpolation(.none).resizable().scaledToFit()
                                        .frame(width: 220, height: 220).padding(12).background(.white)
                                        .accessibilityLabel("一次性配对二维码")
                                }
                                Text(pairing.code).font(.system(.headline, design: .monospaced))
                                Text("剩余 \(max(0, Int(pairing.expiry.timeIntervalSince(context.date)))) 秒").font(.caption).foregroundStyle(.secondary)
                                Button(copied ? "已复制配对码" : "复制配对码") {
                                    UIPasteboard.general.setItems([["public.utf8-plain-text": pairing.code]], options: [.localOnly: true, .expirationDate: pairing.expiry]); copied = true
                                }
                            }.frame(maxWidth: .infinity)
                        } else { Text("配对码已过期，请重新生成。").foregroundStyle(.orange) }
                    }
                    Button("重新生成配对码") { copied = false; Task { await model.createPairing() } }.disabled(model.isManagingDevices)
                    Text("只能使用一次。请仅向自己的安卓手机展示；新码会替换旧码。成功配对新手机后，原安卓手机的转发凭据失效。")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            Section("安卓上的下一步") {
                Link("下载 private-sms-client", destination: URL(string: "https://sms.example.com/download/android.apk")!)
                Text("安装后打开“扫描 iPhone 配对码”，或者手动输入上方代码。阅读上传说明，完成配对、允许短信权限，再开启转发。")
                Text("先发送 App 内测试消息，再验证真实短信和锁屏转发。")
            }.font(.subheadline)
            if let error = model.error { Section { Text(error).font(.footnote).foregroundStyle(.orange) } }
        }
        .navigationTitle("安卓设备").navigationBarTitleDisplayMode(.inline)
        .task {
            while !Task.isCancelled {
                await model.refreshProfile()
                do { try await Task.sleep(for: .seconds(5)) } catch { break }
            }
        }
        .onDisappear { model.pairingCode = nil }
        .confirmationDialog("更换安卓发送端？", isPresented: $confirmReplace, titleVisibility: .visible) {
            Button("生成新配对码") { Task { await model.createPairing() } }
        } message: { Text("新手机成功配对后，旧手机无法继续上传。请先确认旧手机没有待发送短信。") }
        .confirmationDialog("解除安卓配对？", isPresented: $confirmUnpair, titleVisibility: .visible) {
            Button("解除配对", role: .destructive) { Task { await model.unpairAndroid() } }
        } message: { Text("安卓会失去上传权限，服务器中的短信保留。以后可生成新码重新配对。") }
    }

    private func qrCode(_ code: String) -> UIImage? {
        let filter = CIFilter.qrCodeGenerator()
        // The payload is a versioned local data format, never an arbitrary server URL.
        filter.message = Data("private-sms:pair:v1:\(code)".utf8)
        guard let output = filter.outputImage?.transformed(by: CGAffineTransform(scaleX: 10, y: 10)),
              let image = CIContext().createCGImage(output, from: output.extent) else { return nil }
        return UIImage(cgImage: image)
    }
}
