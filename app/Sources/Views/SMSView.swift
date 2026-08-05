import SwiftUI

/// 短信会话：左侧会话列表（按号码分组），右侧聊天界面
struct SMSView: View {
    @EnvironmentObject private var smsStore: SMSStore
    @State private var showCompose = false
    @State private var showClearOptions = false
    @State private var selectedItem: SMSItem?
    @State private var selectedPhone: String?
    @State private var showNotifyDeniedAlert = false

    private var conversations: [Conversation] {
        Dictionary(grouping: smsStore.items) { $0.sender ?? "未知号码" }
            .map { Conversation(phone: $0.key, messages: $0.value.sorted { $0.timestamp < $1.timestamp }) }
            .sorted { $0.lastTimestamp > $1.lastTimestamp }
    }

    private var selectedConversation: Conversation? {
        guard let selectedPhone else { return nil }
        return conversations.first { $0.phone == selectedPhone }
    }

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            Divider()
            HStack(spacing: 0) {
                conversationList
                Divider()
                conversationDetail
            }
        }
        .navigationTitle("短信")
        .onAppear {
            smsStore.refresh()
            openPendingConversation()
        }
        .onChange(of: smsStore.pendingOpenSender) { sender in
            if sender != nil {
                openPendingConversation()
            }
        }
        .sheet(isPresented: $showCompose) { ComposeSMSView(onSent: { smsStore.refresh(force: true) }) }
        .sheet(item: $selectedItem) { item in
            SMSDetailView(item: item, onDelete: { deleted in
                if deleted {
                    selectedItem = nil
                    smsStore.refresh(force: true)
                }
            }, onReply: {
                selectedItem = nil
                selectedPhone = item.sender
                smsStore.refresh(force: true)
            })
        }
        .sheet(isPresented: $showClearOptions) {
            SMSClearView(onDone: { smsStore.refresh(force: true) })
        }
    }

    // MARK: - 工具栏

    private var toolbar: some View {
        HStack(spacing: 10) {
            Text("短信")
                .font(.headline)
            if let count = smsStore.status?.count {
                Text("\(count) 条").font(.callout).foregroundStyle(.secondary)
            }
            if let storage = smsStore.storage {
                storageBadge("SIM 卡", storage.usage?["SM"])
                storageBadge("模块", storage.usage?["ME"])
            }
            if smsStore.status?.polling == true {
                Label("轮询中", systemImage: "arrow.triangle.2.circlepath")
                    .font(.caption).foregroundStyle(.orange)
            }
            Spacer()
            if let error = smsStore.lastError, !error.isEmpty {
                Label(error, systemImage: "exclamationmark.triangle")
                    .font(.caption).foregroundStyle(.red)
                    .lineLimit(1)
            }
            Button {
                smsStore.refresh(force: true)
            } label: {
                if smsStore.busy {
                    ProgressView().controlSize(.small)
                } else {
                    Label("刷新", systemImage: "arrow.clockwise")
                }
            }
            .disabled(smsStore.busy)
            Button {
                toggleNotifications()
            } label: {
                Label("新短信通知", systemImage: smsStore.notificationsEnabled ? "bell.fill" : "bell.slash")
            }
            .help(smsStore.notificationsEnabled ? "关闭新短信系统通知" : "开启新短信系统通知")
            .alert("通知权限被拒绝", isPresented: $showNotifyDeniedAlert) {
                Button("好", role: .cancel) {}
            } message: {
                Text("请在 系统设置 → 通知 中允许 DJOneHub Native 的通知后再开启。")
            }
            Button("清空短信") {
                showClearOptions = true
            }
            Button {
                showCompose = true
            } label: {
                Label("新短信", systemImage: "square.and.pencil")
            }
            .buttonStyle(.borderedProminent)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
    }

    private func storageBadge(_ title: String, _ usage: SMSStorageUsage?) -> some View {
        guard let usage else {
            return AnyView(EmptyView())
        }
        let full = usage.used >= usage.total && usage.total > 0
        return AnyView(
            Text("\(title) \(usage.used)/\(usage.total)")
                .font(.caption)
                .foregroundStyle(full ? Color.red : Color.secondary)
                .padding(.horizontal, 6).padding(.vertical, 2)
                .background(Capsule().fill(full ? Color.red.opacity(0.12) : Color.gray.opacity(0.12)))
        )
    }

    /// 点击通知后打开指定号码的会话
    private func openPendingConversation() {
        guard let sender = smsStore.pendingOpenSender else { return }
        selectedPhone = sender
        smsStore.consumeOpenRequest()
    }

    private func toggleNotifications() {
        let enabling = !smsStore.notificationsEnabled
        smsStore.notificationsEnabled = enabling
        if enabling {
            Task {
                if await !smsStore.ensureAuthorization() {
                    smsStore.notificationsEnabled = false
                    showNotifyDeniedAlert = true
                }
            }
        }
    }

    // MARK: - 会话列表

    private var conversationList: some View {
        List(selection: $selectedPhone) {
            ForEach(conversations) { conversation in
                ConversationRow(conversation: conversation)
                    .tag(conversation.phone)
            }
        }
        .listStyle(.inset)
        .frame(width: 240)
        .scrollIndicators(.visible)
    }

    // MARK: - 会话详情

    @ViewBuilder
    private var conversationDetail: some View {
        if let conversation = selectedConversation {
            ConversationDetailView(
                conversation: conversation,
                onOpenMessage: { selectedItem = $0 },
                onSent: { smsStore.refresh(force: true) })
        } else {
            VStack(spacing: 10) {
                Image(systemName: "message")
                    .font(.system(size: 40)).foregroundStyle(.tertiary)
                Text("选择一个会话查看短信")
                    .font(.callout).foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }
}

/// 会话：按号码分组的短信（消息按时间正序，最新在底部）
struct Conversation: Identifiable {
    let phone: String
    var messages: [SMSItem]

    var id: String { phone }
    var lastMessage: SMSItem? { messages.last }
    var lastTimestamp: Date { messages.last?.timestamp ?? .distantPast }
}

struct ConversationRow: View {
    let conversation: Conversation

    var body: some View {
        HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 3) {
                Text(conversation.phone)
                    .font(.headline)
                    .lineLimit(1)
                if let last = conversation.lastMessage {
                    HStack(spacing: 6) {
                        Text(last.isOutgoing ? "我：" : "")
                            .foregroundStyle(.secondary)
                        Text(last.content ?? "")
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                    .font(.caption)
                }
            }
            Spacer()
            if let last = conversation.lastMessage {
                Text(last.timestamp.formatted(date: .omitted, time: .shortened))
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(.vertical, 4)
    }
}

/// 聊天界面：气泡列表 + 输入框
struct ConversationDetailView: View {
    let conversation: Conversation
    let onOpenMessage: (SMSItem) -> Void
    let onSent: () -> Void

    @State private var draft = ""
    @State private var sending = false

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text(conversation.phone)
                    .font(.headline)
                Spacer()
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)

            Divider()

            ScrollViewReader { proxy in
                ScrollView {
                    VStack(spacing: 8) {
                        ForEach(conversation.messages) { item in
                            MessageBubble(item: item) {
                                onOpenMessage(item)
                            }
                            .id(item.id)
                        }
                    }
                    .padding(16)
                    .frame(maxWidth: .infinity)
                }
                .scrollIndicators(.visible)
                .onAppear {
                    if let last = conversation.messages.last {
                        proxy.scrollTo(last.id, anchor: .bottom)
                    }
                }
                .onChange(of: conversation.messages.count) { _ in
                    if let last = conversation.messages.last {
                        withAnimation {
                            proxy.scrollTo(last.id, anchor: .bottom)
                        }
                    }
                }
            }
            .background(Color(nsColor: .textBackgroundColor))

            Divider()

            HStack(spacing: 10) {
                TextField("短信内容", text: $draft)
                    .textFieldStyle(.roundedBorder)
                    .onSubmit { send() }
                Button {
                    send()
                } label: {
                    if sending {
                        ProgressView().controlSize(.small)
                    } else {
                        Label("发送", systemImage: "paperplane")
                    }
                }
                .buttonStyle(.borderedProminent)
                .disabled(sending || draft.isEmpty)
            }
            .padding(14)
        }
    }

    private func send() {
        let message = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !message.isEmpty, !sending else { return }
        sending = true
        Task {
            do {
                let client = APIClient()
                let _: SMSSendResult = try await client.send(
                    "api/sms/send",
                    body: SMSSendRequest(phone: conversation.phone, message: message))
                await MainActor.run {
                    sending = false
                    draft = ""
                    onSent()
                }
            } catch {
                await MainActor.run {
                    sending = false
                }
            }
        }
    }
}

/// 聊天气泡
struct MessageBubble: View {
    let item: SMSItem
    let onOpen: () -> Void

    private var isOutgoing: Bool { item.isOutgoing }

    var body: some View {
        HStack {
            if isOutgoing {
                Spacer(minLength: 60)
            }
            VStack(alignment: isOutgoing ? .trailing : .leading, spacing: 3) {
                Text(item.content ?? "")
                    .font(.callout)
                    .textSelection(.enabled)
                HStack(spacing: 6) {
                    if let code = item.code, !code.isEmpty {
                        Text("验证码 \(code)")
                            .font(.caption2.bold())
                            .padding(.horizontal, 5).padding(.vertical, 1)
                            .background(Capsule().fill(Color.orange.opacity(0.25)))
                            .foregroundStyle(.orange)
                    }
                    Text(item.timestamp.formatted(date: .omitted, time: .shortened))
                        .font(.caption2)
                        .opacity(0.7)
                }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(
                RoundedRectangle(cornerRadius: 14)
                    .fill(isOutgoing ? Color.accentColor : Color(nsColor: .controlBackgroundColor)))
            .foregroundStyle(isOutgoing ? .white : .primary)
            .overlay(
                RoundedRectangle(cornerRadius: 14)
                    .stroke(isOutgoing ? Color.clear : Color(nsColor: .separatorColor), lineWidth: 1))
            .contentShape(Rectangle())
            .onTapGesture { onOpen() }
            .help("查看详情")
            if !isOutgoing {
                Spacer(minLength: 60)
            }
        }
        .frame(maxWidth: 560)
        .frame(maxWidth: .infinity)
    }
}

/// 短信详情弹窗：查看内容 + 回复 + 逐条删除
struct SMSDetailView: View {
    @Environment(\.dismiss) private var dismiss
    let item: SMSItem
    let onDelete: (Bool) -> Void
    let onReply: () -> Void

    @State private var deleting = false
    @State private var errorMessage: String?
    @State private var showingDeleteConfirm = false
    @State private var showReply = false

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack {
                Text("短信详情").font(.headline)
                Spacer()
                Button("关闭") { dismiss() }
                    .keyboardShortcut(.cancelAction)
            }

            HStack(spacing: 10) {
                Text("发送者").foregroundStyle(.secondary).frame(width: 60, alignment: .leading)
                Text(item.sender ?? "未知号码").textSelection(.enabled)
                Spacer()
                if let code = item.code, !code.isEmpty {
                    Text("验证码 \(code)")
                        .font(.caption.bold())
                        .padding(.horizontal, 6).padding(.vertical, 2)
                        .background(Capsule().fill(Color.orange.opacity(0.2)))
                        .foregroundStyle(.orange)
                }
            }
            .font(.callout)

            HStack(spacing: 10) {
                Text("时间").foregroundStyle(.secondary).frame(width: 60, alignment: .leading)
                Text(item.timestamp.formatted(date: .long, time: .standard)).textSelection(.enabled)
                Spacer()
            }
            .font(.callout)

            if item.isFromModule {
                HStack(spacing: 10) {
                    Text("存储位置").foregroundStyle(.secondary).frame(width: 60, alignment: .leading)
                    Text(item.moduleStorage == "SM" ? "SIM 卡（SM）" : "模块存储（ME）")
                        .font(.callout)
                    Text("索引 \(item.moduleIndex ?? 0)")
                        .font(.caption).foregroundStyle(.secondary)
                    Spacer()
                }
            } else if item.isArchived {
                HStack(spacing: 10) {
                    Text("存储位置").foregroundStyle(.secondary).frame(width: 60, alignment: .leading)
                    Text("本机存储")
                        .font(.callout)
                    Text(item.moduleStorage == "SM" ? "（原来自 SIM 卡）" : item.moduleStorage == "ME" ? "（原来自模块存储）" : "")
                        .font(.caption).foregroundStyle(.secondary)
                    Spacer()
                }
            }

            Divider()
            Text("内容").foregroundStyle(.secondary).font(.caption)
            ScrollView {
                Text(item.content ?? "")
                    .font(.body)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(maxHeight: .infinity)

            if let errorMessage {
                Text(errorMessage).font(.caption).foregroundStyle(.red)
            }

            HStack {
                Button {
                    showReply = true
                } label: {
                    Label("回复", systemImage: "arrowshape.turn.up.left")
                }
                .disabled(deleting)
                Spacer()
                Button("删除此短信", role: .destructive) {
                    showingDeleteConfirm = true
                }
                .buttonStyle(.borderedProminent)
                .disabled(deleting)
            }
        }
        .padding(20)
        .frame(width: 480, height: 400)
        .sheet(isPresented: $showReply) {
            ComposeSMSView(initialPhone: item.sender ?? "", onSent: {
                showReply = false
                onReply()
            })
        }
        .confirmationDialog("删除此短信？", isPresented: $showingDeleteConfirm, titleVisibility: .visible) {
            Button("删除", role: .destructive) { delete() }
            Button("取消", role: .cancel) {}
        } message: {
            Text(deleteMessage)
        }
    }

    private var deleteMessage: String {
        if item.isFromModule {
            return "将从\(item.moduleStorage == "SM" ? "SIM 卡" : "模块存储")中删除，不可恢复。"
        }
        if item.isArchived {
            return "将从本机存储中删除，不可恢复。"
        }
        return "将删除本机缓存中的这条记录。"
    }

    private func delete() {
        deleting = true
        Task {
            do {
                let client = APIClient()
                let request: SMSDeleteRequest
                if item.isFromModule {
                    request = SMSDeleteRequest(storage: item.moduleStorage ?? "ME", index: item.moduleIndex ?? 0)
                } else {
                    request = SMSDeleteRequest(
                        sender: item.sender,
                        content: item.content,
                        timestamp: item.timestamp)
                }
                let result: SMSDeleteResult = try await client.send(
                    "api/sms/delete", body: request)
                deleting = false
                onDelete(result.deleted)
            } catch {
                deleting = false
                errorMessage = "删除失败：\(error.localizedDescription)"
            }
        }
    }
}

/// 清空短信存储：SIM 卡 / 模块 / 本机 可多选
struct SMSClearView: View {
    @Environment(\.dismiss) private var dismiss
    let onDone: () -> Void

    @State private var clearSM = false
    @State private var clearME = false
    @State private var clearLocal = false
    @State private var clearing = false
    @State private var errorMessage: String?

    private var nothingSelected: Bool {
        !clearSM && !clearME && !clearLocal
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("清空短信存储").font(.headline)
            Text("选择要清空的存储位置，可多选。删除后不可恢复。")
                .font(.caption).foregroundStyle(.secondary)

            VStack(alignment: .leading, spacing: 12) {
                Toggle("SIM 卡存储（SM）", isOn: $clearSM)
                Toggle("模块存储（ME）", isOn: $clearME)
                Toggle("本机存储（已保存的短信）", isOn: $clearLocal)
            }

            if let errorMessage {
                Text(errorMessage).font(.callout).foregroundStyle(.red)
            }

            HStack {
                Button("取消") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Spacer()
                Button {
                    clear()
                } label: {
                    if clearing {
                        ProgressView().controlSize(.small)
                    } else {
                        Text("清空")
                    }
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
                .disabled(clearing || nothingSelected)
            }
        }
        .padding(20)
        .frame(width: 400)
    }

    private func clear() {
        clearing = true
        Task {
            do {
                let client = APIClient()
                let _: SMSClearResult = try await client.send(
                    "api/sms/clear",
                    body: SMSClearRequest(sm: clearSM, me: clearME, local: clearLocal))
                clearing = false
                onDone()
                dismiss()
            } catch {
                clearing = false
                errorMessage = "清空失败：\(error.localizedDescription)"
            }
        }
    }
}

/// 发送短信
struct ComposeSMSView: View {
    @Environment(\.dismiss) private var dismiss
    let onSent: () -> Void

    @State private var phone: String
    @State private var message = ""
    @State private var sending = false
    @State private var resultMessage: String?

    init(initialPhone: String = "", onSent: @escaping () -> Void) {
        self.onSent = onSent
        _phone = State(initialValue: initialPhone)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("发送短信").font(.headline)
            TextField("收件人号码（国际号码如 +86138XXXXXXXX）", text: $phone)
                .textFieldStyle(.roundedBorder)
            TextEditor(text: $message)
                .font(.body)
                .frame(minHeight: 120)
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color(nsColor: .separatorColor)))
                .onChange(of: message) { newValue in
                    if newValue.count > 300 {
                        message = String(newValue.prefix(300))
                    }
                }
            Text("\(message.count)/300")
                .font(.caption).foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .trailing)

            if let resultMessage {
                Text(resultMessage)
                    .font(.callout)
                    .foregroundStyle(resultMessage.contains("成功") ? .green : .red)
            }

            HStack {
                Button("取消") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Spacer()
                Button {
                    send()
                } label: {
                    if sending {
                        ProgressView().controlSize(.small)
                    } else {
                        Text("发送")
                    }
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
                .disabled(sending || phone.isEmpty || message.isEmpty)
            }
        }
        .padding(20)
        .frame(width: 460)
    }

    private func send() {
        sending = true
        Task {
            do {
                let client = APIClient()
                let result: SMSSendResult = try await client.send(
                    "api/sms/send",
                    body: SMSSendRequest(phone: phone, message: message))
                await MainActor.run {
                    sending = false
                    resultMessage = result.sent
                        ? "发送成功（\(result.segments ?? 1) 段）"
                        : "发送失败"
                }
                if result.sent {
                    try? await Task.sleep(nanoseconds: 300_000_000)
                    onSent()
                    dismiss()
                }
            } catch {
                await MainActor.run {
                    sending = false
                    resultMessage = "发送失败：\(error.localizedDescription)"
                }
            }
        }
    }
}
