#!/usr/bin/env python3
"""Issue an expiring code; read credential stays on the local machine. No secrets in argv."""
import argparse
import json
import pathlib
import urllib.request

parser = argparse.ArgumentParser(description="为现有用户生成安卓配对码（有效 10 分钟）")
parser.add_argument("--invitation", default="invitations/owner.json")
parser.add_argument("--endpoint", default="http://127.0.0.1:8080", help="HTTPS server origin, or local loopback HTTP")
args = parser.parse_args()
project = pathlib.Path(__file__).resolve().parents[1]
invitation = pathlib.Path(args.invitation)
if not invitation.is_absolute():
    invitation = project / invitation
account = json.loads(invitation.read_text())
# Local-only management request, never print the account's read token.
from urllib.parse import urlsplit
origin = args.endpoint.rstrip("/")
parts = urlsplit(origin)
if (parts.scheme != "https" and not (parts.scheme == "http" and parts.hostname in ("127.0.0.1", "localhost", "::1"))) or not parts.hostname or parts.username or parts.password or parts.path or parts.query or parts.fragment:
    raise SystemExit("Use an HTTPS origin or loopback HTTP origin without credentials or paths.")
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None
opener = urllib.request.build_opener(NoRedirect())
request = urllib.request.Request(origin + "/v1/android/pairing", data=b"{}", method="POST", headers={"Authorization": "Bearer " + account["read_token"], "Content-Type": "application/json", "User-Agent": "private-sms-admin/0.1"})
try:
    with opener.open(request, timeout=15) as response:
        result = json.load(response)
except Exception:
    raise SystemExit("生成失败：请确认服务地址、读取令牌和网络连接。")
print("安卓配对码：", result["code"])
print("有效期 10 分钟，仅可用一次；不要公开分享。")
