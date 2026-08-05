import SwiftUI

/// 首页：模块状态总览（紧凑排版，数据来自共享缓存 DashboardStore）
struct HomeView: View {
    @EnvironmentObject private var backend: BackendProcess
    @EnvironmentObject private var store: DashboardStore
    @EnvironmentObject private var smsStore: SMSStore
    @Environment(\.colorScheme) private var colorScheme

    @State private var showingRebootConfirm = false
    @State private var copiedValue: String?
    @State private var draggedService: Int?
    @State private var showNotifyDeniedAlert = false
    @StateObject private var ringtonePreview = RingtonePreview()

    private var status: DeviceStatus? { store.status }
    private var health: HealthStatus? { store.health }
    private var traffic: TrafficSnapshot? { store.traffic }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                switch backend.state {
                case .running:
                    if let status {
                        overviewBar
                        deviceCard
                        WaterfallLayout(minColumnWidth: 320, spacing: 12) {
                            trafficCard
                            smsCard
                            priorityCard
                            voiceCard
                        }
                    } else if store.statusStale {
                        VStack(spacing: 10) {
                            Image(systemName: "exclamationmark.triangle.fill")
                                .font(.system(size: 34)).foregroundStyle(.orange)
                            Text("模块状态获取失败").font(.headline)
                            Text("请确认模块已连接，然后重试。").font(.callout).foregroundStyle(.secondary)
                            Button("重试") {
                                store.refresh()
                            }
                            .buttonStyle(.borderedProminent)
                        }
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 40)
                    } else {
                        HStack(spacing: 10) {
                            ProgressView().controlSize(.small)
                            Text("读取模块状态…").foregroundStyle(.secondary)
                        }
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 60)
                    }
                case .starting:
                    VStack(spacing: 10) {
                        ProgressView().controlSize(.small)
                        Text("正在启动服务…").foregroundStyle(.secondary)
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 60)
                case .stopped:
                    VStack(spacing: 10) {
                        Image(systemName: "power").font(.system(size: 34)).foregroundStyle(.tertiary)
                        Text("服务未运行").font(.headline)
                        Text("请退出并重新打开应用").font(.callout).foregroundStyle(.secondary)
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 60)
                case .failed(let reason):
                    VStack(spacing: 10) {
                        Image(systemName: "exclamationmark.triangle.fill")
                            .font(.system(size: 34)).foregroundStyle(.orange)
                        Text("服务启动失败").font(.headline)
                        Text(reason)
                            .font(.callout).foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                        Button("重试") {
                            backend.start()
                        }
                        .buttonStyle(.borderedProminent)
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 40)
                }
            }
            .padding(16)
            .frame(maxWidth: 780)
            .frame(maxWidth: .infinity)
        }
        .scrollIndicators(.visible)
        .overlay(alignment: .bottomTrailing) {
            if let toast = store.toast {
                ToastBubble(toast: toast) {
                    store.toast = nil
                }
                .onHover { hovering in
                    if hovering {
                        store.extendToast()
                    }
                }
                .padding(16)
                .transition(.move(edge: .bottom).combined(with: .opacity))
            }
        }
        .animation(.easeInOut(duration: 0.25), value: store.callStatus)
        .animation(.easeInOut(duration: 0.25), value: store.toast)
        .onAppear { store.loadServices() }
        .confirmationDialog("重启模块？", isPresented: $showingRebootConfirm, titleVisibility: .visible) {
            Button("重启（AT+CFUN=1,1）", role: .destructive) { store.reboot() }
            Button("取消", role: .cancel) {}
        } message: {
            Text("模块重启会导致网络临时中断，正在进行的下载或写入操作会被打断。")
        }
    }

    // MARK: - 顶部概览条

    private var overviewBar: some View {
        HStack(spacing: 14) {
            if let detail = status?.hardwareStatus, !detail.isEmpty {
                VStack(alignment: .leading, spacing: 3) {
                    Text("未检测到模块").font(.headline)
                    Text(detail)
                        .font(.caption).foregroundStyle(.secondary)
                        .lineLimit(2)
                }
                Spacer()
            } else {
                VStack(alignment: .leading, spacing: 3) {
                    Text(status?.operatorName ?? "-")
                        .font(.headline)
                    Text("\(status?.regStatusText ?? "-") · 更新于 \(store.lastUpdated?.formatted(date: .omitted, time: .standard) ?? "-")")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Spacer()

                if store.statusStale {
                    Text("数据已过期")
                        .font(.caption.bold())
                        .padding(.horizontal, 6).padding(.vertical, 2)
                        .background(Capsule().fill(Color.orange.opacity(0.15)))
                        .foregroundStyle(.orange)
                }

                if let dbm = status?.signalDbm {
                    HStack(spacing: 6) {
                        signalBars(dbm: dbm)
                        Text("\(dbm) dBm").font(.callout.monospacedDigit())
                    }
                }

                if let mode = status?.networkMode, !mode.isEmpty {
                    Text(mode)
                        .font(.caption.bold())
                        .padding(.horizontal, 8).padding(.vertical, 3)
                        .background(Capsule().fill(Color.accentColor.opacity(0.15)))
                        .foregroundStyle(Color.accentColor)
                }

                HStack(spacing: 5) {
                    Circle()
                        .fill(status?.simInserted == true ? Color.green : Color.orange)
                        .frame(width: 7, height: 7)
                    Text(status?.simInserted == true ? "SIM 已插入" : "SIM 未插入")
                        .font(.caption)
                }
                .foregroundStyle(.secondary)

                Button("重启模块") {
                    showingRebootConfirm = true
                }
                .controlSize(.small)
                .disabled(store.busy)
            }
        }
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 10).fill(
            colorScheme == .dark
                ? Color(nsColor: .controlBackgroundColor)
                : Color.accentColor.opacity(0.06)))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color(nsColor: .separatorColor), lineWidth: 1))
    }

    // MARK: - 设备信息

    private var deviceCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            sectionTitle("设备信息")
            LazyVGrid(columns: [GridItem(.flexible(), spacing: 16), GridItem(.flexible(), spacing: 16)], spacing: 6) {
                row("IMEI", status?.imei ?? "-")
                row("固件", status?.firmware ?? "-")
                row("ICCID", status?.iccid ?? "-")
                row("IMSI", status?.imsi ?? "-")
                row("AT 端口", health?.port ?? "-")
                row("eSIM 管理", health?.esimAvailable == true ? "可用" : "不可用")
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .topLeading)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color(nsColor: .separatorColor), lineWidth: 1))
    }

    // MARK: - 实时流量

    private var trafficCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 10) {
                sectionTitle("网络与流量")
                Spacer()
                if store.busy {
                    ProgressView().controlSize(.small)
                }
                Button {
                    store.runCheck4G()
                } label: {
                    Text("检查 4G 出口")
                }
                .controlSize(.small)
                .disabled(store.busy)
                Button(store.usbnetEnabled ? "关闭网卡" : "开启网卡") {
                    store.usbnetEnabled.toggle()
                    store.switchMode()
                }
                .controlSize(.small)
                .disabled(store.busy || !store.usbnetLoaded)
            }
            if let traffic {
                HStack(spacing: 16) {
                    trafficItem("本次下载", trafficMetric(traffic.sessionRX))
                    Divider().frame(height: 26)
                    trafficItem("本次上传", trafficMetric(traffic.sessionTX))
                    Divider().frame(height: 26)
                    trafficItem("本次总流量", trafficMetric(traffic.sessionTotal))
                    Divider().frame(height: 26)
                    trafficItem("网卡累计", trafficMetric(trafficTotal))
                }
            } else {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("读取流量数据…").foregroundStyle(.secondary)
                }
                .padding(.vertical, 12)
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .topLeading)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color(nsColor: .separatorColor), lineWidth: 1))
    }

    // MARK: - 网卡优先级

    private var priorityCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 10) {
                sectionTitle("网卡优先级")
                Spacer()
                Button("应用顺序") {
                    store.saveOrder()
                }
                .controlSize(.small)
                .disabled(store.services.count < 2)
                Button {
                    store.loadServices()
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .controlSize(.small)
                .help("刷新服务列表")
            }
            if store.servicesLoaded {
                if store.services.isEmpty {
                    Text("未读取到网络服务").font(.caption).foregroundStyle(.secondary)
                } else {
                    VStack(spacing: 4) {
                        ForEach(Array(store.services.enumerated()), id: \.element.id) { index, service in
                            ServiceRowView(
                                index: index,
                                service: service,
                                isDragging: draggedService == index)
                            .onDrag {
                                draggedService = index
                                return NSItemProvider(object: service.name as NSString)
                            } preview: {
                                Text(service.name)
                                    .font(.callout)
                                    .padding(.horizontal, 12).padding(.vertical, 6)
                                    .background(RoundedRectangle(cornerRadius: 6).fill(Color(nsColor: .controlBackgroundColor)))
                                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.accentColor, lineWidth: 1))
                            }
                            .onDrop(
                                of: [.text],
                                delegate: ServiceDropDelegate(
                                    destination: index,
                                    dragged: $draggedService,
                                    onMove: { from, to in store.moveService(from, to: to) }))
                        }
                    }
                    HStack(spacing: 10) {
                        if let orderMessage = store.orderMessage {
                            Text(orderMessage).font(.caption).foregroundStyle(.green)
                        }
                        if let orderError = store.orderError {
                            Text(orderError).font(.caption).foregroundStyle(.red)
                                .lineLimit(2)
                        }
                        Spacer()
                    }
                }
            } else {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("读取网络服务…").foregroundStyle(.secondary)
                }
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .topLeading)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color(nsColor: .separatorColor), lineWidth: 1))
    }

    // MARK: - 短信保存

    private var smsCard: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("短信保存").font(.callout.bold())
            switchRow("接管模块短信", isOn: Binding(
                get: { store.smsAdopt },
                set: { store.setSMSAdopt($0) }
            ))
            switchRow("新短信通知", isOn: $smsStore.notificationsEnabled) {
                requestNotificationAuth()
            }
            if let storage = smsStore.storage {
                HStack(spacing: 8) {
                    Text("存储").font(.caption).foregroundStyle(.secondary)
                    storageBadge("SIM 卡", storage.usage?["SM"])
                    storageBadge("模块", storage.usage?["ME"])
                }
            }
            Text(store.smsAdopt
                 ? "已接管：收到的短信将保存到本机，并自动清理 SIM 卡与模块中的原始短信。"
                 : "未接管：短信保留在 SIM 卡与模块存储中，由模块存储管理。")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color(nsColor: .separatorColor), lineWidth: 1))
        .alert("通知权限被拒绝", isPresented: $showNotifyDeniedAlert) {
            Button("好", role: .cancel) {}
        } message: {
            Text("请在 系统设置 → 通知 中允许 DJOneHub 的通知后再开启。")
        }
    }

    /// 标题在左、开关在右的一行
    private func switchRow(_ title: String, isOn: Binding<Bool>, onChange: (() -> Void)? = nil) -> some View {
        HStack(spacing: 8) {
            Text(title).font(.callout)
            Spacer()
            Toggle("", isOn: isOn)
                .toggleStyle(.switch)
                .controlSize(.small)
                .labelsHidden()
                .onChange(of: isOn.wrappedValue) { on in
                    if on { onChange?() }
                }
        }
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

    private func requestNotificationAuth() {
        Task {
            if await !smsStore.ensureAuthorization() {
                smsStore.notificationsEnabled = false
                showNotifyDeniedAlert = true
            }
        }
    }

    // MARK: - 语音通话

    private var voiceCard: some View {
        VStack(alignment: .leading, spacing: 8) {
            // 标题行：状态徽标 + 通话操作
            HStack(spacing: 10) {
                Text("语音通话").font(.callout.bold())
                Spacer()
                statusBadge
                if !store.callStatus.isIdle && store.callStatus.state != "unknown" {
                    Button("查看详情") {
                        store.showCallDetail = true
                    }
                    .controlSize(.small)
                    Button("挂断", role: .destructive) {
                        store.hangup()
                    }
                    .controlSize(.small)
                }
            }

            // 启用语音
            switchRow("启用语音", isOn: Binding(
                get: { store.voiceEnabled },
                set: { store.setVoiceEnabled($0) }
            ))

            // 来电铃声选择 + 试听
            HStack(spacing: 8) {
                Text("来电铃声").font(.callout)
                Spacer()
                Picker("", selection: Binding(
                    get: { Ringtones.selectedID() },
                    set: {
                        ringtonePreview.stop()
                        Ringtones.setSelected($0)
                    }
                )) {
                    ForEach(Ringtones.all) { ringtone in
                        Text(ringtone.displayName).tag(ringtone.id)
                    }
                }
                .labelsHidden()
                .pickerStyle(.menu)
                .controlSize(.small)
                Button {
                    ringtonePreview.toggle()
                } label: {
                    Image(systemName: ringtonePreview.isPlaying ? "stop.circle.fill" : "play.circle.fill")
                        .font(.title3)
                }
                .buttonStyle(.plain)
                .disabled(Ringtones.selectedID() == Ringtones.silentID)
                .help(ringtonePreview.isPlaying ? "停止试听" : (Ringtones.selectedID() == Ringtones.silentID ? "已选择静音，无铃声可试听" : "试听当前铃声"))
            }

            // 通话中的动态信息
            if let number = store.callStatus.number, !number.isEmpty {
                infoRow("号码", number)
            }
            if let incomingAt = store.incomingAt {
                infoRow("来电时间", incomingAt.formatted(date: .omitted, time: .standard))
            }
            if let error = store.voiceError {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            // 通话记录（单独一行）
            Button {
                store.showCallHistory = true
            } label: {
                Label("通话记录（\(store.callHistory.count)）", systemImage: "clock.arrow.circlepath")
            }
            .buttonStyle(.bordered)
            .controlSize(.small)

            Text("当前模块固件不支持通话音频传输，仅支持来电与通话状态查看。")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color(nsColor: .separatorColor), lineWidth: 1))
        .sheet(isPresented: $store.showCallHistory) {
            CallHistoryView()
        }
    }

    /// 标签左对齐、值可复制的信息行
    private func infoRow(_ title: String, _ value: String) -> some View {
        HStack(spacing: 10) {
            Text(title).foregroundStyle(.secondary).frame(width: 56, alignment: .leading)
            Text(value).textSelection(.enabled)
            Spacer()
        }
        .font(.callout)
    }

    private var statusBadge: some View {
        Text(callStatusText)
            .font(.caption.bold())
            .padding(.horizontal, 8).padding(.vertical, 3)
            .background(Capsule().fill(statusDotColor.opacity(0.15)))
            .foregroundStyle(statusDotColor)
    }

    private var statusDotColor: Color {
        switch store.callStatus.state {
        case "active": return .green
        case "incoming": return .red
        case "dialing", "alerting": return .orange
        default: return .gray
        }
    }

    private var callStatusText: String {
        switch store.callStatus.state {
        case "active": return "通话中"
        case "incoming": return "来电"
        case "dialing": return "拨号中"
        case "alerting": return "呼叫中"
        case "unknown": return "状态未知"
        default: return "空闲"
        }
    }

    // MARK: - 组件

    private func sectionTitle(_ title: String) -> some View {
        Text(title).font(.callout.bold())
    }

    private func row(_ key: String, _ value: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text(key)
                .foregroundStyle(.secondary)
                .frame(width: 56, alignment: .leading)
            Button {
                copyValue(value)
            } label: {
                Text(copiedValue == value ? "已复制 ✓" : value)
                    .foregroundStyle(copiedValue == value ? Color.green : Color.primary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("点击复制")
        }
        .font(.callout)
    }

    private func copyValue(_ value: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(value, forType: .string)
        copiedValue = value
        Task {
            try? await Task.sleep(nanoseconds: 1_500_000_000)
            if copiedValue == value {
                copiedValue = nil
            }
        }
    }

    private func trafficItem(_ title: String, _ value: String) -> some View {
        VStack(spacing: 3) {
            Text(title).font(.caption).foregroundStyle(.secondary)
            Text(value).font(.headline.monospacedDigit())
                .lineLimit(1)
                .minimumScaleFactor(0.7)
        }
        .frame(maxWidth: .infinity)
    }

    private func signalBars(dbm: Int) -> some View {
        let level: Int
        switch dbm {
        case ..<(-100): level = 0
        case ..<(-90): level = 1
        case ..<(-80): level = 2
        case ..<(-65): level = 3
        default: level = 4
        }
        return HStack(alignment: .bottom, spacing: 2) {
            ForEach(0..<4, id: \.self) { i in
                RoundedRectangle(cornerRadius: 1)
                    .fill(i < level ? Color.green : Color.gray.opacity(0.35))
                    .frame(width: 3, height: CGFloat(5 + i * 3))
            }
        }
    }

    private func formatBytes(_ bytes: UInt64?) -> String {
        guard let bytes else { return "-" }
        let formatter = ByteCountFormatter()
        formatter.countStyle = .binary
        formatter.allowedUnits = [.useKB, .useMB, .useGB]
        return formatter.string(fromByteCount: Int64(bytes))
    }

    /// 网卡不可用时显示 "-"，避免后端返回的 0 值造成误导
    private func trafficMetric(_ bytes: UInt64?) -> String {
        guard traffic?.available == true else { return "-" }
        return formatBytes(bytes)
    }

    private var trafficTotal: UInt64? {
        guard let rx = traffic?.rxBytes, let tx = traffic?.txBytes else { return nil }
        return rx + tx
    }
}

/// 来电/通话详情弹窗
struct CallDetailView: View {
    @EnvironmentObject private var store: DashboardStore
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack {
                Text("通话详情").font(.headline)
                Spacer()
                Button("关闭") { dismiss() }
                    .keyboardShortcut(.cancelAction)
            }

            HStack(spacing: 10) {
                Text("号码").foregroundStyle(.secondary).frame(width: 60, alignment: .leading)
                Text(store.callStatus.number ?? "-").textSelection(.enabled)
                Spacer()
            }
            .font(.callout)

            HStack(spacing: 10) {
                Text("状态").foregroundStyle(.secondary).frame(width: 60, alignment: .leading)
                Text(callStatusText(store.callStatus.state))
                    .foregroundStyle(statusColor(store.callStatus.state))
                Spacer()
            }
            .font(.callout)

            if let incomingAt = store.incomingAt {
                HStack(spacing: 10) {
                    Text("来电时间").foregroundStyle(.secondary).frame(width: 60, alignment: .leading)
                    Text(incomingAt.formatted(date: .long, time: .standard)).textSelection(.enabled)
                    Spacer()
                }
                .font(.callout)
            }

            Divider()
            Text("当前模块固件不支持通话音频传输，仅支持来电与通话状态查看。")
                .font(.caption)
                .foregroundStyle(.secondary)

            HStack {
                if !store.callStatus.isIdle && store.callStatus.state != "unknown" {
                    Button("挂断", role: .destructive) {
                        store.hangup()
                        dismiss()
                    }
                    .buttonStyle(.borderedProminent)
                }
                Spacer()
            }
        }
        .padding(20)
        .frame(width: 420)
    }

    private func callStatusText(_ state: String) -> String {
        switch state {
        case "active": return "通话中"
        case "incoming": return "来电"
        case "dialing": return "拨号中"
        case "alerting": return "呼叫中"
        case "unknown": return "状态未知"
        default: return "空闲"
        }
    }

    private func statusColor(_ state: String) -> Color {
        switch state {
        case "active": return .green
        case "incoming": return .red
        case "dialing", "alerting": return .orange
        default: return .primary
        }
    }
}

/// 提示气泡
struct ToastBubble: View {
    let toast: ToastItem
    let onClose: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: toast.icon ?? (toast.isSuccess ? "checkmark.circle.fill" : "xmark.circle.fill"))
                .foregroundStyle(toast.isSuccess ? .green : .red)
            VStack(alignment: .leading, spacing: 2) {
                Text(toast.title ?? (toast.isSuccess ? "检查完成" : "检查失败"))
                    .font(.callout.bold())
                Text(toast.message)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }
            Spacer(minLength: 8)
            Button {
                onClose()
            } label: {
                Image(systemName: "xmark")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .buttonStyle(.plain)
            .help("关闭")
        }
        .padding(12)
        .frame(maxWidth: 360, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .windowBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color(nsColor: .separatorColor), lineWidth: 1))
        .shadow(color: .black.opacity(0.15), radius: 8, y: 2)
    }
}

/// 网卡优先级单行视图
struct ServiceRowView: View {
    let index: Int
    let service: NetworkService
    let isDragging: Bool

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: "line.3.horizontal")
                .font(.caption)
                .foregroundStyle(.tertiary)
            Text("\(index + 1)")
                .font(.callout.monospacedDigit())
                .foregroundStyle(.secondary)
                .frame(width: 18, alignment: .leading)
            Text(service.name)
                .font(.callout)
                .lineLimit(1)
                .truncationMode(.middle)
            badge
            Spacer()
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
        .background(
            RoundedRectangle(cornerRadius: 6)
                .fill(isDragging ? Color.accentColor.opacity(0.12) : Color.clear))
        .contentShape(Rectangle())
    }

    @ViewBuilder
    private var badge: some View {
        if service.module == true {
            Text("模块网卡")
                .font(.caption2.bold())
                .padding(.horizontal, 5).padding(.vertical, 1)
                .background(Capsule().fill(Color.green.opacity(0.18)))
                .foregroundStyle(.green)
        } else if service.usb == true {
            Text("USB")
                .font(.caption2.bold())
                .padding(.horizontal, 5).padding(.vertical, 1)
                .background(Capsule().fill(Color.accentColor.opacity(0.15)))
                .foregroundStyle(Color.accentColor)
        }
    }
}

/// 网卡优先级行的拖拽排序代理
struct ServiceDropDelegate: DropDelegate {
    let destination: Int
    @Binding var dragged: Int?
    let onMove: (Int, Int) -> Void

    func dropEntered(info: DropInfo) {
        guard let from = dragged, from != destination else { return }
        withAnimation(.easeOut(duration: 0.12)) {
            onMove(from, destination)
        }
        dragged = destination
    }

    func dropUpdated(info: DropInfo) -> DropProposal? {
        DropProposal(operation: .move)
    }

    func performDrop(info: DropInfo) -> Bool {
        dragged = nil
        return true
    }
}

/// 瀑布流布局：按可用宽度自适应分列，每个子视图放入当前最矮的一列，保持各自自然高度
struct WaterfallLayout: Layout {
    var minColumnWidth: CGFloat
    var spacing: CGFloat = 12

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? 780
        let columns = columnCount(for: width)
        let columnWidth = (width - CGFloat(columns - 1) * spacing) / CGFloat(columns)
        var heights = [CGFloat](repeating: 0, count: columns)
        for subview in subviews {
            let size = subview.sizeThatFits(ProposedViewSize(width: columnWidth, height: nil))
            let index = heights.indices.min { heights[$0] < heights[$1] }!
            heights[index] += size.height + spacing
        }
        let height = (heights.max() ?? 0) - (subviews.isEmpty ? 0 : spacing)
        return CGSize(width: width, height: max(0, height))
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let columns = columnCount(for: bounds.width)
        let columnWidth = (bounds.width - CGFloat(columns - 1) * spacing) / CGFloat(columns)
        var xOffsets = (0..<columns).map { CGFloat($0) * (columnWidth + spacing) }
        var heights = [CGFloat](repeating: 0, count: columns)
        for subview in subviews {
            let size = subview.sizeThatFits(ProposedViewSize(width: columnWidth, height: nil))
            let index = heights.indices.min { heights[$0] < heights[$1] }!
            subview.place(
                at: CGPoint(x: bounds.minX + xOffsets[index], y: bounds.minY + heights[index]),
                proposal: ProposedViewSize(size))
            heights[index] += size.height + spacing
        }
    }

    private func columnCount(for width: CGFloat) -> Int {
        max(1, Int((width + spacing) / (minColumnWidth + spacing)))
    }
}
