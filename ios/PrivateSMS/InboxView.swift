import SwiftUI
import UIKit

// Select the property-wrapper type explicitly; newer SDKs also export a State macro.
private typealias ViewState<Value> = SwiftUI.State<Value>

struct InboxView: View {
    @ObservedObject var model: InboxModel
    @ViewState<Bool> private var showSettings = false
    @ViewState<String> private var search = ""
    @ViewState<Bool> private var showDemo = false

    private var filtered: [SMSMessage] {
        if search.isEmpty { return model.messages }
        return model.messages.filter { $0.sender.localizedCaseInsensitiveContains(search) || $0.content.localizedCaseInsensitiveContains(search) }
    }

    var body: some View {
        Group {
            if !model.isUnlocked {
                VStack(spacing: 24) {
                    Image(systemName: "lock.shield.fill").font(.system(size: 58)).foregroundStyle(.teal)
                    Text("private-sms-server").font(.largeTitle.bold())
                    Text("来自备用手机，只在这里查看。")
                        .foregroundStyle(.secondary)
                    Button("解锁收件箱", systemImage: "faceid") { Task { await model.unlock() } }
                        .buttonStyle(.borderedProminent).tint(.teal)
                    if let error = model.error { Text(error).font(.footnote).foregroundStyle(.secondary) }
                }.padding(30)
            } else {
                NavigationStack {
                    Group {
                        if !model.hasConnection {
                            ContentUnavailableView {
                                Label("你的私人收件箱", systemImage: "tray.and.arrow.down.fill")
                            } description: {
                                Text("查看配对发送端新收到的短信。需要兼容的发送端手机、配套客户端，以及自己或可信管理员提供的服务地址和个人读取令牌。请先查看兼容性与配对指南。")
                            } actions: {
                                Button("连接短信服务") { showSettings = true }.buttonStyle(.borderedProminent).tint(.teal)
                                Button("先看看演示") { showDemo = true }
                                Link("兼容性、账户与配对指南", destination: AppConfiguration.helpURL)
                                if AppConfiguration.appleLoginEndpoint != nil {
                                    NavigationLink("通过 Apple 登录") { AppleAccountView(model: model) }
                                }
                            }
                        } else {
                            List {
                                Section {
                                    HStack(alignment: .top, spacing: 12) {
                                        Image(systemName: "lock.shield").foregroundStyle(.teal).font(.title2)
                                        VStack(alignment: .leading, spacing: 4) {
                                            Text("短信正文不进入苹果推送").font(.subheadline.weight(.semibold))
                                            Text(pushSummary).font(.caption).foregroundStyle(.secondary)
                                        }
                                    }.padding(.vertical, 5)
                                }
                                if let error = model.error {
                                    Section { Label(error, systemImage: "exclamationmark.circle").font(.footnote).foregroundStyle(.orange) }
                                }
                                Section("收件箱 · \(model.unreadCount) 条未读") {
                                    if filtered.isEmpty {
                                        Text(model.isLoading ? "正在同步…" : (search.isEmpty ? "还没有短信。配对兼容的发送端手机并开启转发后，新短信会出现在这里。" : "已加载的短信中没有匹配项。"))
                                            .foregroundStyle(.secondary).padding(.vertical, 18)
                                    }
                                    ForEach(filtered) { message in
                                        NavigationLink(value: MessageRoute(message: message)) { MessageRow(message: message) }
                                    }
                                    if !model.nextBefore.isEmpty {
                                        Button(model.isLoading ? "正在加载…" : "加载更早的短信") { Task { await model.loadMore() } }
                                            .disabled(model.isLoading)
                                    }
                                }
                                Section {
                                    Text("仅在本次解锁期间保留内存副本。搜索范围为已加载短信。")
                                        .font(.caption).foregroundStyle(.secondary)
                                }
                            }
                            .refreshable { await model.refresh() }
                            .searchable(text: $search, prompt: "搜索已加载的短信")
                        }
                    }
                    .navigationTitle("private-sms-server")
                    .toolbar {
                        ToolbarItem(placement: .topBarTrailing) {
                            Button("设置", systemImage: "gearshape") { showSettings = true }
                        }
                        ToolbarItem(placement: .topBarLeading) {
                            if model.hasConnection { Button("锁定", systemImage: "lock") { model.lock() } }
                        }
                    }
                    .navigationDestination(for: MessageRoute.self) { route in MessageDetail(message: route.message, model: model) }
                }
                .sheet(isPresented: $showSettings) { SettingsView(model: model) }
                .sheet(isPresented: $showDemo) { DemoInboxView() }
            }
        }
        .tint(.teal)
        .onChange(of: model.isUnlocked) { _, unlocked in if !unlocked { showSettings = false; search = "" } }
    }

    private var pushSummary: String {
        guard let status = model.status else { return "下拉同步，或前往设置检查连接。" }
        if !status.apnsConfigured { return "服务端尚未配置 APNs，当前只能打开应用同步。" }
        if !status.deviceRegistered { return "通知设备未注册，请在设置中开启。" }
        if !status.lastPushError.isEmpty { return "推送暂未成功，请检查服务端与设备配置。" }
        return status.pendingNotifications > 0 ? "有 \(status.pendingNotifications) 条提醒等待推送。" : "已连接 · 通知不显示短信正文"
    }
}

private struct MessageRow: View {
    let message: SMSMessage
    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            HStack {
                if message.isUnread { Circle().fill(.teal).frame(width: 7, height: 7).accessibilityLabel("未读") }
                Text(message.sender).font(.body.weight(message.isUnread ? .bold : .regular))
                Spacer()
                Text(message.dateLabel).font(.caption2).foregroundStyle(.secondary)
            }
            Text(message.content).font(.subheadline.weight(message.isUnread ? .bold : .regular)).lineLimit(2)
                .foregroundStyle(message.isUnread ? Color.primary : Color.secondary)
            if !message.sim.isEmpty { Text(message.sim).font(.caption2).foregroundStyle(.teal) }
        }.padding(.vertical, 6)
    }
}

private struct MessageDetail: View {
    let message: SMSMessage
    @ObservedObject var model: InboxModel
    @Environment(\.dismiss) private var dismiss
    @ViewState<Bool> private var confirmDelete = false
    @ViewState<Bool> private var copied = false
    @ViewState<Bool> private var deleting = false

    var body: some View {
        List {
            Section {
                LabeledContent("发件人", value: message.sender)
                LabeledContent("收录时间", value: message.dateLabel)
                if !message.sim.isEmpty { LabeledContent("SIM", value: message.sim) }
                if !message.sourceTime.isEmpty { LabeledContent("手机接收时间", value: message.sourceTime) }
            }
            Section("短信正文") { Text(message.content).font(.body).padding(.vertical, 8) }
            Section {
                if let code = message.verificationCode {
                    Button("复制验证码 \(code)", systemImage: "number") { copy(code) }
                }
                Button("复制全文", systemImage: "doc.on.doc") { copy(message.content) }
                if copied { Text("已复制，仅供本机使用，90 秒后过期。验证码提取结果请核对。")
                    .font(.caption).foregroundStyle(.secondary) }
            }
            Section {
                Button("删除这条短信", role: .destructive) { confirmDelete = true }.disabled(deleting)
                if let error = model.error {
                    Text(error).font(.footnote).foregroundStyle(.orange)
                    Button("重试同步已读状态") { model.beginReading(message) }
                }
            }
        }
        .navigationTitle("短信详情").navigationBarTitleDisplayMode(.inline)
        // The model owns this operation; popping the detail must not cancel its receipt.
        .onAppear { model.beginReading(message) }
        .confirmationDialog("从服务器删除这条短信？删除后无法恢复。", isPresented: $confirmDelete, titleVisibility: .visible) {
            Button("删除短信", role: .destructive) {
                Task {
                    deleting = true
                    await model.delete(message)
                    deleting = false
                    if !model.messages.contains(where: { $0.id == message.id }) { dismiss() }
                }
            }
        }
    }

    private func copy(_ value: String) {
        UIPasteboard.general.setItems([["public.utf8-plain-text": value]], options: [.localOnly: true, .expirationDate: Date().addingTimeInterval(90)])
        copied = true
    }
}

private struct SettingsView: View {
    @ObservedObject var model: InboxModel
    @Environment(\.dismiss) private var dismiss
    @ViewState<String> private var endpoint = AppConfiguration.defaultEndpoint
    @ViewState<String> private var token = ""
    @ViewState<Bool> private var confirmClearMessages = false
    @ViewState<Bool> private var confirmLogout = false

    var body: some View {
        NavigationStack {
            Form {
                if !model.hasConnection && AppConfiguration.appleLoginEndpoint != nil {
                    Section { NavigationLink("通过 Apple 登录／开通") { AppleAccountView(model: model) } }
                }
                Section {
                    TextField("https://你的服务地址", text: $endpoint)
                        .textInputAutocapitalization(.never).autocorrectionDisabled().keyboardType(.URL)
                    SecureField("读取令牌 READ_TOKEN", text: $token)
                        .textInputAutocapitalization(.never).autocorrectionDisabled()
                    Button(model.isConnecting ? "正在连接…" : "验证并保存") {
                        Task {
                            if await model.connect(endpoint: endpoint, token: token) { token = ""; dismiss() }
                        }
                    }.disabled(model.isConnecting || endpoint.isEmpty || token.isEmpty)
                } header: { Text("连接服务") } footer: {
                    Text("向可信服务管理员获取个人读取令牌，或按帮助文档自行部署。令牌是账户凭据，请妥善保存；它不同于设备配对码。保存在本机钥匙串，不同步到 iCloud。修改连接时请重新填写。")
                }
                if let error = model.error { Section { Text(error).foregroundStyle(.orange).font(.footnote) } }
                if model.hasConnection {
                    Section("设备") { NavigationLink("发送端设备与配对") { SenderDevicesView(model: model) } }
                    Section("通知") {
                        Text(model.notificationStatus).font(.subheadline)
                        Button("开启／重新注册通知") { Task { await model.registerNotifications(requestPermission: true) } }
                        Button("打开系统设置") {
                            if let url = URL(string: UIApplication.openSettingsURLString) { UIApplication.shared.open(url) }
                        }
                        if let status = model.status {
                            LabeledContent("服务端 APNs", value: status.apnsConfigured ? "已配置" : "未配置")
                            LabeledContent("等待推送", value: "\(status.pendingNotifications)")
                        }
                    }
                    Section {
                        Button(model.isClearingMessages ? "正在清空短信…" : "清空全部短信", role: .destructive) { confirmClearMessages = true }
                            .disabled(model.isClearingMessages)
                    } header: { Text("短信管理") } footer: {
                        Text("只清空当前账户的短信。保留连接设置、设备配对和推送通知，之后仍可接收新短信。")
                    }
                    Section("账户管理") {
                        if model.profile?.appleLinked == true || model.usesAppleLogin {
                            Label("已绑定 Apple 登录", systemImage: "checkmark.shield")
                            if AppConfiguration.appleLoginEndpoint != nil {
                                NavigationLink("重新通过 Apple 登录") { AppleAccountView(model: model) }
                            }
                            Button("退出登录") { confirmLogout = true }
                        } else if AppConfiguration.appleLoginEndpoint != nil && model.endpoint == APIClient.serviceEndpoint && model.profile?.appleAvailable == true {
                            NavigationLink("绑定 Apple 账户，支持换机恢复") { AppleAccountView(model: model, linking: true) }
                        }
                        NavigationLink("注销账户") { AccountDeletionView(model: model) }
                            .disabled(model.isClearingMessages)
                    }
                }
                Section("隐私与运行条件") {
                    Text("苹果推送只包含通用提醒。短信经自己的服务端处理并加密保存；此版本不是端到端加密。")
                    Text("发送端、服务端和网络需要保持在线。首次连接后，可通过配套客户端发送测试消息，验证此设备能否收到提醒。")
                }.font(.footnote).foregroundStyle(.secondary)
                Section("帮助与隐私") {
                    Link("连接与配对指南", destination: AppConfiguration.helpURL)
                    Link("部署自己的短信服务", destination: AppConfiguration.setupURL)
                    Link("隐私政策", destination: AppConfiguration.privacyURL)
                    Text("仅同步兼容发送端开启转发后新收到的 SMS。本 App 只用于接收，不读取本机系统短信，也不能作为短信发送端；不支持通话、彩信或 RCS。")
                        .font(.footnote).foregroundStyle(.secondary)
                }
            }
            .navigationTitle("设置").navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("完成") { dismiss() } } }
            .onAppear { if !model.endpoint.isEmpty { endpoint = model.endpoint } }
            .onDisappear { token = "" }
            .onChange(of: model.hasConnection) { _, connected in if !connected { dismiss() } }
            .confirmationDialog("退出当前设备？", isPresented: $confirmLogout, titleVisibility: .visible) {
                Button("退出登录", role: .destructive) { Task { await model.logout() } }
            } message: { Text("保留短信和设备配对，暂停此设备的推送。使用同一个 Apple 账户可重新登录。") }
            .confirmationDialog("清空当前账户的全部短信？", isPresented: $confirmClearMessages, titleVisibility: .visible) {
                Button("清空短信", role: .destructive) { Task { _ = await model.clearMessages() } }
                Button("取消", role: .cancel) { }
            } message: {
                Text("删除的短信无法恢复。账户、连接设置、设备配对和通知保持有效。")
            }
        }
    }
}

private struct AccountDeletionView: View {
    @ObservedObject var model: InboxModel
    @Environment(\.dismiss) private var dismiss
    @ViewState<String> private var confirmation = ""
    @ViewState<Bool> private var deleting = false

    var body: some View {
        Form {
            Section {
                Text("注销会永久删除账户和全部短信，并使读取令牌、发送端上传权限和推送注册失效。之后需要重新开通账户。")
                Text("如果只想清理短信，请返回上一页使用“清空全部短信”。")
            }
            Section {
                TextField("输入“注销账户”确认", text: $confirmation)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                    .disabled(deleting)
                Button(deleting ? "正在注销…" : "永久注销账户", role: .destructive) {
                    Task {
                        deleting = true
                        if await model.deleteAccount() { dismiss() }
                        deleting = false
                    }
                }.disabled(deleting || confirmation != "注销账户")
            } header: { Text("确认注销") } footer: { Text("此操作无法撤销。") }
            if let error = model.error { Section { Text(error).foregroundStyle(.orange).font(.footnote) } }
        }
        .navigationTitle("注销账户").navigationBarTitleDisplayMode(.inline)
    }
}

// Available to every user before connecting. All messages are synthetic and stay in memory.
private struct DemoInboxView: View {
    @Environment(\.dismiss) private var dismiss
    @ViewState<[SMSMessage]> private var messages = Self.samples
    @ViewState<String> private var search = ""
    @ViewState<Bool> private var copied = false

    private static var samples: [SMSMessage] {
        let date = ISO8601DateFormatter().string(from: Date())
        return [
            SMSMessage(id: "demo-code", sender: "示例服务", content: "【演示短信】你的验证码是 246810，5 分钟内有效。这是虚构内容，不用于任何真实账户。", sim: "备用手机 · SIM 1", sourceTime: "", receivedAt: date, read: false),
            SMSMessage(id: "demo-delivery", sender: "示例快递", content: "【演示短信】你的包裹已送达，请在方便时领取。所有信息均为示例。", sim: "备用手机 · SIM 1", sourceTime: "", receivedAt: date, read: false),
            SMSMessage(id: "demo-reminder", sender: "示例提醒", content: "【演示短信】明天下午 3 点的预约已确认。这条消息展示已读短信的样式。", sim: "备用手机 · SIM 1", sourceTime: "", receivedAt: date, read: true)
        ]
    }

    var body: some View {
        NavigationStack {
            List {
                Section {
                    Label("演示模式 · 无需连接服务器", systemImage: "play.rectangle")
                        .font(.subheadline.weight(.semibold))
                    Text("以下均为虚构短信。可以查看详情、体验已读变化与验证码复制，不访问你的短信或发送网络请求。")
                        .font(.footnote).foregroundStyle(.secondary)
                }
                Section("收件箱 · \(messages.filter(\.isUnread).count) 条未读") {
                    ForEach(messages.filter { search.isEmpty || $0.sender.localizedCaseInsensitiveContains(search) || $0.content.localizedCaseInsensitiveContains(search) }) { message in
                        NavigationLink(value: message.id) { MessageRow(message: message) }
                    }
                }
                Section {
                    Text("实际使用时，兼容的发送端手机需要开机、联网并开启转发。本机通知仅显示通用提醒和未读角标。")
                        .font(.footnote).foregroundStyle(.secondary)
                }
            }
            .navigationTitle("演示收件箱")
            .searchable(text: $search, prompt: "搜索演示短信")
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("完成") { dismiss() } } }
            .navigationDestination(for: String.self) { id in
                if let message = messages.first(where: { $0.id == id }) {
                    List {
                        Section {
                            Label("演示数据", systemImage: "play.rectangle").foregroundStyle(.teal)
                            LabeledContent("发件人", value: message.sender)
                            LabeledContent("收录时间", value: message.dateLabel)
                            LabeledContent("SIM", value: message.sim)
                        }
                        Section("短信正文") { Text(message.content).padding(.vertical, 8) }
                        Section {
                            if let code = message.verificationCode {
                                Button("复制验证码 \(code)", systemImage: "number") { copy(code) }
                            }
                            Button("复制全文", systemImage: "doc.on.doc") { copy(message.content) }
                            if copied { Text("已复制，仅供本机使用，90 秒后过期。") .font(.footnote).foregroundStyle(.secondary) }
                        }
                        Section { Text("返回列表后，这条短信会显示为已读。演示不会更改真实账户或 App 角标。") .font(.footnote).foregroundStyle(.secondary) }
                    }
                    .navigationTitle("短信详情").navigationBarTitleDisplayMode(.inline)
                    .onAppear {
                        copied = false
                        if let index = messages.firstIndex(where: { $0.id == id }) { messages[index].read = true }
                    }
                }
            }
        }.tint(.teal)
    }

    private func copy(_ value: String) {
        UIPasteboard.general.setItems([["public.utf8-plain-text": value]], options: [.localOnly: true, .expirationDate: Date().addingTimeInterval(90)])
        copied = true
    }
}
