import AppKit
import SwiftUI
import UniformTypeIdentifiers

// THESIS: 通话页是一张桌面工作台，不再把拨号、准备、记录和备份堆成同权重卡片。
// OWN-WORLD: 原生 macOS 双区布局；连续操作面、细分隔线、系统字体与语义色。
// STORY: 用户先看见号码与最近通话，需要时输入并拨打；运行证据始终留在右侧检查器。
// FIRST VIEWPORT: 宽屏左侧承载拨号与铃声、右侧承载准备与备份；默认宽度按当前任务纵向排序。
// FORM: grounded structure 7, surface seed 5d9b737a; approved comp call-workspace-b.png.
// FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, and DESIGN.md
struct CallView: View {
    @EnvironmentObject private var store: DashboardStore
    @EnvironmentObject private var attentionStore: AttentionStore
    @StateObject private var ringtonePreview = RingtonePreview()

    @AppStorage("callModeDownloadSource") private var downloadSource = "relay"
    @State private var showUSBConfirmation = false
    @State private var showDownloadConfirmation = false
    @State private var hasPromptedForDownload = false
    @State private var showAllHistory = false
    @State private var showAllBackups = false
    @State private var backupToRestore: CallModeUSBBackupSummary?
    @State private var showRestoreConfirmation = false
    @State private var backupToDelete: CallModeUSBBackupSummary?
    @State private var showDeleteConfirmation = false
    @State private var layoutMode = CallLayoutMode.compact

    private let dialKeys: [DialKey] = [
        .init(symbol: "1", letters: ""),
        .init(symbol: "2", letters: "ABC"),
        .init(symbol: "3", letters: "DEF"),
        .init(symbol: "4", letters: "GHI"),
        .init(symbol: "5", letters: "JKL"),
        .init(symbol: "6", letters: "MNO"),
        .init(symbol: "7", letters: "PQRS"),
        .init(symbol: "8", letters: "TUV"),
        .init(symbol: "9", letters: "WXYZ"),
        .init(symbol: "*", letters: ""),
        .init(symbol: "0", letters: "+"),
        .init(symbol: "#", letters: ""),
    ]

    private var mode: CallModeStatus? { store.callModeStatus }
    private var callReady: Bool { mode?.isReady == true }

    var body: some View {
        GeometryReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    pageHeader
                    responsiveCallContent
                }
                .frame(maxWidth: .infinity)
                .padding(18)
                .frame(maxWidth: 1120)
                .frame(maxWidth: .infinity)
            }
            .scrollIndicators(.visible)
            .preference(
                key: CallLayoutModePreferenceKey.self,
                value: CallLayoutMode.resolve(width: max(0, proxy.size.width - 36)))
        }
        .animation(.easeInOut(duration: 0.2), value: store.callStatus.state)
        .onPreferenceChange(CallLayoutModePreferenceKey.self) { newMode in
            guard layoutMode != newMode else { return }
            var transaction = Transaction()
            transaction.disablesAnimations = true
            withTransaction(transaction) {
                layoutMode = newMode
            }
        }
        .onAppear {
            attentionStore.setViewingCalls(true)
            store.loadCallHistory()
            store.pollCallModeStatus()
            store.loadCallModeBackups()
            promptForDownloadIfNeeded(mode?.state)
        }
        .onDisappear {
            attentionStore.setViewingCalls(false)
            ringtonePreview.stop()
        }
        .onChange(of: mode?.state) { state in
            promptForDownloadIfNeeded(state)
        }
        .confirmationDialog(
            usbConfirmationTitle,
            isPresented: $showUSBConfirmation,
            titleVisibility: .visible
        ) {
            Button(usbConfirmationActionTitle) {
                store.enableCallMode(
                    confirmADBAuthorization: mode?.requiresADBAuthorizationConfirmation == true)
            }
            Button("取消", role: .cancel) {}
        } message: {
            Text(usbConfirmationMessage)
        }
        .confirmationDialog(
            "下载模块侧语音运行时？",
            isPresented: $showDownloadConfirmation,
            titleVisibility: .visible
        ) {
            Button("下载并自动准备") {
                store.downloadCallModeRuntime(source: downloadSource)
            }
            Button("取消", role: .cancel) {}
        } message: {
            Text(downloadConfirmationMessage)
        }
        .confirmationDialog(
            "还原模块原始配置？",
            isPresented: $showRestoreConfirmation,
            titleVisibility: .visible
        ) {
            if let backup = backupToRestore {
                Button("还原并重启模块", role: .destructive) {
                    store.restoreCallModeBackup(backup)
                    backupToRestore = nil
                }
            }
            Button("取消", role: .cancel) {
                backupToRestore = nil
            }
        } message: {
            Text(restoreConfirmationMessage)
        }
        .confirmationDialog(
            "删除配置备份？",
            isPresented: $showDeleteConfirmation,
            titleVisibility: .visible
        ) {
            if let backup = backupToDelete {
                Button("删除备份", role: .destructive) {
                    store.deleteCallModeBackup(backup)
                    backupToDelete = nil
                }
            }
            Button("取消", role: .cancel) {
                backupToDelete = nil
            }
        } message: {
            Text(deleteConfirmationMessage)
        }
        .sheet(isPresented: $showAllHistory) {
            CallHistoryView()
        }
    }

    @ViewBuilder
    private var pageHeader: some View {
        switch layoutMode {
        case .wide, .compact:
            HStack(alignment: .firstTextBaseline, spacing: 14) {
                Text("通话")
                    .font(.title2.bold())
                Spacer(minLength: 18)
                pageStatusLabel
                    .lineLimit(1)
                    .truncationMode(.tail)
            }
        case .stacked:
            VStack(alignment: .leading, spacing: 5) {
                Text("通话")
                    .font(.title2.bold())
                pageStatusLabel
            }
        }
    }

    private var pageStatusLabel: some View {
        Label(pageStatusText, systemImage: modeBadgeIcon)
            .font(.callout)
            .foregroundStyle(modeBadgeColor)
            .accessibilityLabel("通话模式：\(pageStatusText)")
    }

    @ViewBuilder
    private var responsiveCallContent: some View {
        switch layoutMode {
        case .wide:
            HStack(alignment: .top, spacing: 14) {
                VStack(alignment: .leading, spacing: 14) {
                    callWorkspace
                    ringtoneOptionCard
                }
                .frame(minWidth: 540, maxWidth: .infinity, alignment: .topLeading)

                VStack(alignment: .leading, spacing: 14) {
                    operationalInspector
                    backupOptionCard
                }
                .frame(width: 340, alignment: .topLeading)
            }
        case .compact, .stacked:
            VStack(alignment: .leading, spacing: 14) {
                if preparationLeads {
                    compactOperationalInspector
                }
                callWorkspace
                ringtoneOptionCard
                if !preparationLeads {
                    compactOperationalInspector
                }
                backupOptionCard
            }
        }
    }

    private var callWorkspace: some View {
        VStack(spacing: 0) {
            HStack(alignment: .center, spacing: 10) {
                Text(callPanelTitle)
                    .font(.headline)
                Spacer()
                callStateBadge
            }
            .padding(14)

            Divider()

            if let callError, !callError.isEmpty {
                callErrorBanner(callError)
                    .padding(14)
                Divider()
            }

            Group {
                if store.callStatus.isIdle {
                    dialerWorkbench
                } else if store.callStatus.state == "unknown" {
                    unknownCallState
                } else {
                    liveCall
                }
            }
            .padding(14)

            Divider()
            audioDeviceControls
                .padding(14)
        }
        .frame(maxWidth: .infinity, alignment: .topLeading)
        .background(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .stroke(Color(nsColor: .separatorColor), lineWidth: 1))
    }

    private var dialerWorkbench: some View {
        VStack(alignment: .leading, spacing: 14) {
            numberEntry
            Text("拨分机可输入“主号,分机”。接通后每个逗号暂停 2 秒，也可在通话中使用按键盘。")
                .font(.caption)
                .foregroundStyle(.secondary)
            responsiveDialerContent
        }
    }

    @ViewBuilder
    private var responsiveDialerContent: some View {
        switch layoutMode {
        case .wide:
            HStack(alignment: .top, spacing: 14) {
                recentCalls
                    .frame(minWidth: 280, maxWidth: .infinity, alignment: .topLeading)
                Divider()
                keypad
                    .frame(width: 220, alignment: .top)
            }
        case .compact:
            HStack(alignment: .top, spacing: 10) {
                recentCalls
                    .frame(minWidth: 250, maxWidth: .infinity, alignment: .topLeading)
                Divider()
                keypad
                    .frame(width: 190, alignment: .top)
            }
        case .stacked:
            VStack(alignment: .leading, spacing: 14) {
                keypad
                    .frame(maxWidth: 360)
                    .frame(maxWidth: .infinity)
                Divider()
                recentCalls
            }
        }
    }

    private var numberEntry: some View {
        HStack(spacing: 8) {
            TextField("输入电话号码", text: $store.dialNumber)
                .textFieldStyle(.roundedBorder)
                .font(.title3.monospacedDigit())
                .onSubmit { dial() }
                .accessibilityLabel("电话号码")

            Button("+") {
                appendPlus()
            }
            .controlSize(.large)
            .disabled(store.dialNumber.contains("+"))
            .help("添加国际号码前缀")
            .accessibilityLabel("添加国际号码前缀")

            Button(",") {
                store.dialNumber.append(",")
            }
            .controlSize(.large)
            .disabled(store.dialNumber.isEmpty)
            .help("添加 2 秒暂停，之后输入分机号码")
            .accessibilityLabel("添加两秒暂停")

            Button {
                if !store.dialNumber.isEmpty {
                    store.dialNumber.removeLast()
                }
            } label: {
                Image(systemName: "delete.left")
            }
            .controlSize(.large)
            .disabled(store.dialNumber.isEmpty)
            .help("删除最后一位")
            .accessibilityLabel("删除最后一位")
        }
    }

    private var keypad: some View {
        VStack(spacing: 10) {
            dialKeyGrid(inCall: false)

            Button {
                dial()
            } label: {
                Label("拨打电话", systemImage: "phone.fill")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            .tint(.green)
            .controlSize(.large)
            .keyboardShortcut(.defaultAction)
            .disabled(!canDial)
            .help(dialHelp)

            if !callReady {
                Label("完成右侧准备后即可拨号", systemImage: "lock.fill")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }

    private func dialKeyGrid(inCall: Bool) -> some View {
        Grid(horizontalSpacing: 8, verticalSpacing: 8) {
            ForEach(0..<4, id: \.self) { row in
                GridRow {
                    ForEach(0..<3, id: \.self) { column in
                        let key = dialKeys[row * 3 + column]
                        Button {
                            if inCall {
                                store.sendDTMF(key.symbol)
                            } else {
                                store.dialNumber.append(key.symbol)
                            }
                        } label: {
                            VStack(spacing: 1) {
                                Text(key.symbol)
                                    .font(.title3.monospacedDigit())
                                Text(key.letters.isEmpty || (inCall && key.symbol == "0") ? " " : key.letters)
                                    .font(.system(size: 8, weight: .medium))
                                    .foregroundStyle(.secondary)
                                    .tracking(0.7)
                            }
                            .frame(maxWidth: .infinity, minHeight: 38)
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.bordered)
                        .foregroundStyle(.primary)
                        .disabled(inCall && (store.callStatus.dtmfAvailable != true || store.callHangupInFlight))
                        .accessibilityLabel(inCall ? "发送按键 \(key.symbol)" : "输入 \(key.symbol)")
                    }
                }
            }
        }
    }

    private var recentCalls: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .firstTextBaseline) {
                Text("最近通话")
                    .font(.headline)
                Spacer()
                Button("全部 \(store.callHistory.count)") {
                    showAllHistory = true
                }
                .controlSize(.small)
                .disabled(store.callHistory.isEmpty)
            }
            .padding(.bottom, 6)

            if store.callHistory.isEmpty {
                VStack(spacing: 7) {
                    Image(systemName: "phone")
                        .font(.title2)
                        .foregroundStyle(.tertiary)
                        .accessibilityHidden(true)
                    Text("暂无通话记录")
                        .font(.callout.weight(.medium))
                    Text("完成的呼叫会显示在这里")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, minHeight: 190)
            } else {
                VStack(spacing: 0) {
                    ForEach(Array(store.callHistory.prefix(5))) { record in
                        recentCallRow(record)
                        if record.id != store.callHistory.prefix(5).last?.id {
                            Divider()
                        }
                    }
                }
            }
        }
    }

    private func recentCallRow(_ record: CallRecord) -> some View {
        let number = record.callbackNumber?.trimmingCharacters(in: .whitespacesAndNewlines)

        return HStack(spacing: 10) {
            Image(systemName: recentCallIcon(record))
                .foregroundStyle(recentCallColor(record))
                .frame(width: 20)
                .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: 2) {
                Text(number?.isEmpty == false ? number! : "未知号码")
                    .font(.callout)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .textSelection(.enabled)
                Text(recentCallMetadata(record))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer(minLength: 8)

            Text(recentCallKind(record))
                .font(.caption)
                .foregroundStyle(record.isMissed ? Color.red : Color.secondary)

            Button {
                redial(record)
            } label: {
                Image(systemName: "phone.fill")
                    .frame(width: 18, height: 18)
            }
            .buttonStyle(.plain)
            .foregroundStyle(canRedial(record) ? Color.accentColor : Color.secondary)
            .disabled(!canRedial(record))
            .help(redialHelp(record))
            .accessibilityLabel(number.map { "回拨 \($0)" } ?? "号码不可用")
        }
        .padding(.vertical, 9)
        .contentShape(Rectangle())
        .contextMenu {
            if canRedial(record), let number, !number.isEmpty {
                Button {
                    redial(record)
                } label: {
                    Label("拨打 \(number)", systemImage: "phone.fill")
                }
            }
            Button(role: .destructive) {
                store.deleteCallRecord(record.id)
            } label: {
                Label("删除这条记录", systemImage: "trash")
            }
        }
        .accessibilityElement(children: .contain)
    }

    private var unknownCallState: some View {
        VStack(spacing: 11) {
            Image(systemName: "questionmark.circle")
                .font(.system(size: 34))
                .foregroundStyle(.orange)
                .accessibilityHidden(true)
            Text("正在确认通话状态")
                .font(.headline)
            Text("状态未知时不会发起新的呼叫。请重新同步模块状态后再继续。")
                .font(.callout)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            Button("重新同步") {
                store.pollCallStatus()
            }
            .buttonStyle(.borderedProminent)
        }
        .frame(maxWidth: .infinity, minHeight: 300)
    }

    private var liveCall: some View {
        VStack(spacing: 16) {
            Image(systemName: store.callStatus.isIncoming ? "phone.arrow.down.left.fill" : "phone.fill")
                .font(.system(size: 42))
                .foregroundStyle(callStateColor)
                .accessibilityHidden(true)

            VStack(spacing: 5) {
                Text(store.callStatus.number ?? "未知号码")
                    .font(.title.bold().monospacedDigit())
                    .textSelection(.enabled)
                Text(callStateText)
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }

            if store.callStatus.isActive {
                Label(audioStatusText, systemImage: audioStatusIcon)
                    .font(.callout)
                    .foregroundStyle(store.audioRunning ? Color.green : Color.secondary)
            }

            HStack(spacing: 12) {
                if store.callStatus.isIncoming {
                    Button {
                        store.answerCall()
                    } label: {
                        HStack(spacing: 7) {
                            if store.callAnswerInFlight {
                                ProgressView()
                                    .controlSize(.small)
                            } else {
                                Image(systemName: "phone.fill")
                            }
                            Text(store.callAnswerInFlight ? "正在接听" : "接听")
                        }
                        .frame(minWidth: 84)
                    }
                    .buttonStyle(.borderedProminent)
                    .tint(.green)
                    .controlSize(.large)
                    .disabled(!callReady || store.callAnswerInFlight)
                    .help(callReady ? "接听来电" : "请先完成通话模式准备")
                }

                Button(role: .destructive) {
                    store.hangup()
                } label: {
                    Label("挂断", systemImage: "phone.down.fill")
                        .frame(minWidth: 84)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(store.callHangupInFlight)
            }

            if store.callStatus.isActive {
                inCallKeypad
            }
        }
        .frame(maxWidth: .infinity, minHeight: 340)
    }

    private var inCallKeypad: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("通话按键")
                .font(.headline)
            dialKeyGrid(inCall: true)
            if store.callStatus.postDialState == "sending" {
                Label("正在发送分机，请稍候…", systemImage: "clock")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else if let error = store.dtmfError ?? store.callStatus.postDialError {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
            } else if store.dtmfPendingCount > 0 {
                Text("待发送 \(store.dtmfPendingCount) 个按键")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                Text("按键会发送给对方，用于分机和语音菜单。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .frame(maxWidth: 280)
    }

    private var audioDeviceControls: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("通话音频")
                .font(.callout.weight(.medium))
            audioDevicePicker("麦克风", devices: store.callAudio.inputs,
                              uid: store.callAudio.inputUID, savedName: store.callAudio.inputName,
                              select: store.selectCallAudioInput)
            audioDevicePicker("扬声器／耳机", devices: store.callAudio.outputs,
                              uid: store.callAudio.outputUID, savedName: store.callAudio.outputName,
                              select: store.selectCallAudioOutput)

            if store.audioRunning,
               let input = store.callAudio.activeInputName, let output = store.callAudio.activeOutputName {
                Text("输入：\(input) · 输出：\(output)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Label("回声消除与自动增益已启用", systemImage: "waveform")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else if store.callStatus.isActive, let message = store.callAudio.message {
                Text(message)
                    .font(.caption)
                    .foregroundStyle(.primary)
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                Text("仅影响 DJOneHub，输入和输出可以使用不同设备。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if store.callStatus.isActive && (audioNeedsAttention || store.audioError != nil) {
                Button("重新连接音频") { store.retryCallAudio() }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func audioDevicePicker(_ title: String, devices: [CallAudioDevice], uid: String,
                                   savedName: String, select: @escaping (String) -> Void) -> some View {
        Picker(title, selection: Binding(get: { uid }, set: select)) {
            Text("跟随系统默认").tag("")
            if !uid.isEmpty && !devices.contains(where: { $0.uid == uid }) {
                Text("\(savedName.isEmpty ? "已保存设备" : savedName)（未连接）").tag(uid)
            }
            ForEach(devices) { device in
                Text(device.name).tag(device.uid)
            }
        }
        .pickerStyle(.menu)
        .frame(maxWidth: .infinity, alignment: .leading)
        .help("仅改变 DJOneHub 的\(title)，不会更改系统默认设备。")
    }

    private var audioNeedsAttention: Bool {
        store.callAudio.phase == .waitingForDevice || store.callAudio.phase == .failed
    }

    private var audioStatusText: String {
        if store.audioRunning { return "Mac 与模块音频已连接" }
        if store.audioError != nil { return "通话音频未连接" }
        switch store.callAudio.phase {
        case .waitingForDevice: return "音频暂停 · 等待设备连接"
        case .failed: return "通话音频未连接"
        case .idle, .connecting, .running: return "正在建立通话音频…"
        }
    }

    private var audioStatusIcon: String {
        if store.audioRunning { return "waveform.circle.fill" }
        return audioNeedsAttention || store.audioError != nil ? "exclamationmark.circle" : "waveform.circle"
    }

    private func callErrorBanner(_ error: String) -> some View {
        HStack(alignment: .top, spacing: 9) {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(.red)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 3) {
                Text("通话操作未完成")
                    .font(.callout.weight(.medium))
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .combine)
    }

    private var operationalInspector: some View {
        VStack(spacing: 0) {
            inspectorHeader
                .padding(14)

            Divider()

            VStack(spacing: 0) {
                readinessRow(
                    title: "模块接口",
                    detail: interfaceDetail,
                    state: interfaceStepState)
                Divider().padding(.leading, 29)
                readinessRow(
                    title: "运行时下载",
                    detail: runtimeDetail,
                    state: runtimeStepState)
                Divider().padding(.leading, 29)
                readinessRow(
                    title: "模块部署",
                    detail: deploymentDetail,
                    state: deploymentStepState)
            }
            .padding(.horizontal, 14)

            if hasPreparationDetail {
                Divider()
                preparationDetail
                    .padding(14)
            }

        }
        .frame(maxWidth: .infinity, alignment: .topLeading)
        .background(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .stroke(Color(nsColor: .separatorColor), lineWidth: 1))
    }

    private var compactOperationalInspector: some View {
        VStack(spacing: 0) {
            inspectorHeader
                .padding(12)

            Divider()

            VStack(spacing: 0) {
                compactReadinessRow(title: "模块接口", state: interfaceStepState)
                Divider().padding(.leading, 27)
                compactReadinessRow(title: "运行时下载", state: runtimeStepState)
                Divider().padding(.leading, 27)
                compactReadinessRow(title: "模块部署", state: deploymentStepState)
            }
            .padding(.horizontal, 12)

            if hasPreparationDetail {
                Divider()
                preparationDetail
                    .padding(12)
            }
        }
        .frame(maxWidth: .infinity, alignment: .topLeading)
        .background(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .stroke(Color(nsColor: .separatorColor), lineWidth: 1))
    }

    private var ringtoneOptionCard: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 10) {
                Text("来电铃声")
                    .font(.callout.bold())
                Spacer(minLength: 14)
                ringtoneControls
            }

            Text("来电时由 Mac 播放；静音不会影响模块侧呼叫状态。")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .topLeading)
        .background(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .stroke(Color(nsColor: .separatorColor), lineWidth: 1))
    }

    private var backupOptionCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 10) {
                Text("模块配置备份")
                    .font(.callout.bold())
                Spacer()
                if !store.callModeBackups.isEmpty {
                    Text("\(store.callModeBackups.count) 项")
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
                if store.callModeBackupImporting {
                    ProgressView()
                        .controlSize(.small)
                        .accessibilityLabel("正在导入配置备份")
                } else {
                    Button {
                        chooseImportFile()
                    } label: {
                        Label("导入…", systemImage: "tray.and.arrow.down")
                    }
                    .controlSize(.small)
                    .disabled(
                        store.callModeBackupExportingID != nil
                            || store.callModeBackupDeletingID != nil)
                }
                Button {
                    store.loadCallModeBackups()
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .controlSize(.small)
                .disabled(
                    store.callModeBackupsLoading
                        || store.callModeBackupImporting
                        || store.callModeBackupDeletingID != nil)
                .help("刷新配置备份")
                .accessibilityLabel("刷新配置备份")
            }

            backupContent
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .topLeading)
        .background(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .stroke(Color(nsColor: .separatorColor), lineWidth: 1))
    }

    private var inspectorHeader: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .top, spacing: 9) {
                Image(systemName: modeBadgeIcon)
                    .font(.title3)
                    .foregroundStyle(modeBadgeColor)
                    .frame(width: 22)
                    .accessibilityHidden(true)

                VStack(alignment: .leading, spacing: 3) {
                    Text(mode?.summary ?? "正在检查通话模式")
                        .font(.headline)
                    if let detail = mode?.detail, !detail.isEmpty {
                        Text(detail)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            }

            modeAction
                .frame(maxWidth: .infinity, alignment: .trailing)
        }
    }

    @ViewBuilder
    private var modeAction: some View {
        if mode?.isBusy == true || store.callModeActionInFlight {
            HStack(spacing: 7) {
                ProgressView()
                    .controlSize(.small)
                Text("正在准备…")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .accessibilityLabel(mode?.summary ?? "正在准备通话模式")
        } else {
            switch mode?.state {
            case "needs_adb_authorization", "needs_usb", "needs_voice":
                Button("开启通话模式") {
                    showUSBConfirmation = true
                }
                .buttonStyle(.borderedProminent)
            case "needs_interface_recovery":
                Button("重启并修复接口") {
                    showUSBConfirmation = true
                }
                .buttonStyle(.borderedProminent)
            case "needs_download":
                Button("下载运行时") {
                    showDownloadConfirmation = true
                }
                .buttonStyle(.borderedProminent)
            case "failed":
                Button("重新检查并准备") {
                    store.retryCallModePreparation()
                }
                .buttonStyle(.borderedProminent)
            case "disconnected", "unsupported":
                Button("重新检测") {
                    store.retryCallModePreparation()
                }
            case "ready":
                EmptyView()
            default:
                Button("检查") {
                    store.pollCallModeStatus()
                }
            }
        }
    }

    private func readinessRow(title: String, detail: String, state: ReadinessState) -> some View {
        HStack(alignment: .top, spacing: 9) {
            Group {
                switch state {
                case .complete:
                    Image(systemName: "checkmark.circle.fill")
                        .foregroundStyle(.green)
                case .actionRequired:
                    Image(systemName: "arrow.right.circle.fill")
                        .foregroundStyle(Color.accentColor)
                case .current:
                    ProgressView()
                        .controlSize(.mini)
                case .failed:
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                case .pending:
                    Image(systemName: "circle")
                        .foregroundStyle(.tertiary)
                }
            }
            .frame(width: 20, height: 20)
            .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: 2) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text(title)
                        .font(.callout.weight(.medium))
                    Spacer()
                    Text(state.accessibilityText)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Text(detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.vertical, 10)
        .accessibilityElement(children: .combine)
    }

    private func compactReadinessRow(title: String, state: ReadinessState) -> some View {
        HStack(spacing: 7) {
            Group {
                switch state {
                case .complete:
                    Image(systemName: "checkmark.circle.fill")
                        .foregroundStyle(.green)
                case .actionRequired:
                    Image(systemName: "arrow.right.circle.fill")
                        .foregroundStyle(Color.accentColor)
                case .current:
                    ProgressView()
                        .controlSize(.mini)
                case .failed:
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                case .pending:
                    Image(systemName: "circle")
                        .foregroundStyle(.tertiary)
                }
            }
            .frame(width: 20, height: 20)
            .accessibilityHidden(true)

            Text(title)
                .font(.callout.weight(.medium))
            Spacer(minLength: 6)
            Text(state.accessibilityText)
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .padding(.vertical, 9)
        .accessibilityElement(children: .combine)
    }

    private var preparationDetail: some View {
        VStack(alignment: .leading, spacing: 10) {
            if mode?.state == "downloading", let mode {
                VStack(alignment: .leading, spacing: 5) {
                    ProgressView(value: mode.downloadProgress)
                    HStack {
                        Text(byteProgress(mode))
                        Spacer()
                        Text("校验后保存")
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                }
            }

            if mode?.state == "needs_download" {
                VStack(alignment: .leading, spacing: 6) {
                    Picker("下载渠道", selection: $downloadSource) {
                        ForEach(mode?.sources ?? []) { source in
                            Text(source.name).tag(source.id)
                        }
                    }
                    .pickerStyle(.menu)

                    if let selected = selectedDownloadSource {
                        Text(selected.detail)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            }

            if let error = store.callModeError, !error.isEmpty {
                HStack(alignment: .top, spacing: 8) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                        .accessibilityHidden(true)
                    Text(error)
                        .font(.caption)
                        .textSelection(.enabled)
                }
                .accessibilityElement(children: .combine)
            }

            if let mode, !mode.runtimePath.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    Text("运行时 \(mode.runtimeVersion)")
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                    HStack(spacing: 10) {
                        Button("Finder 中显示") {
                            revealPath(mode.runtimePath)
                        }
                        .buttonStyle(.link)
                        .controlSize(.small)
                        Button("固定版本") {
                            openURL(mode.upstream)
                        }
                        .buttonStyle(.link)
                        .controlSize(.small)
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var ringtoneControls: some View {
        HStack(spacing: 8) {
            Picker("来电铃声", selection: Binding(
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

            Button {
                ringtonePreview.toggle()
            } label: {
                Image(systemName: ringtonePreview.isPlaying ? "stop.circle.fill" : "play.circle.fill")
            }
            .disabled(Ringtones.selectedID() == Ringtones.silentID)
            .help(ringtonePreview.isPlaying ? "停止试听" : "试听当前铃声")
            .accessibilityLabel(ringtonePreview.isPlaying ? "停止试听" : "试听当前铃声")
        }
        .fixedSize(horizontal: true, vertical: false)
    }

    private var backupContent: some View {
        VStack(alignment: .leading, spacing: 11) {
            Text("修改 USB 配置前自动保存；可导入已导出的 JSON，只允许还原到同一模块。")
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            if let error = store.callModeBackupError, !error.isEmpty {
                HStack(alignment: .top, spacing: 8) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                        .accessibilityHidden(true)
                    VStack(alignment: .leading, spacing: 6) {
                        Text(error)
                            .font(.caption)
                            .textSelection(.enabled)
                        Button("重试") {
                            store.loadCallModeBackups()
                        }
                        .controlSize(.small)
                    }
                }
            }

            if store.callModeBackupsLoading && store.callModeBackups.isEmpty {
                HStack(spacing: 8) {
                    ProgressView()
                        .controlSize(.small)
                    Text("正在读取配置备份…")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, minHeight: 48, alignment: .leading)
            } else if store.callModeBackups.isEmpty && store.callModeBackupError == nil {
                HStack(alignment: .top, spacing: 9) {
                    Image(systemName: "externaldrive.badge.timemachine")
                        .foregroundStyle(.secondary)
                        .accessibilityHidden(true)
                    VStack(alignment: .leading, spacing: 2) {
                        Text("还没有配置备份")
                            .font(.callout.weight(.medium))
                        Text("首次开启通话模式时会自动创建，也可以从 JSON 文件导入。")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                .frame(maxWidth: .infinity, minHeight: 48, alignment: .leading)
            } else {
                VStack(spacing: 0) {
                    ForEach(visibleBackups) { backup in
                        backupRow(backup)
                        if backup.id != visibleBackups.last?.id {
                            Divider()
                        }
                    }
                }

                if store.callModeBackups.count > 3 {
                    Button(showAllBackups ? "收起较早备份" : "显示其余 \(store.callModeBackups.count - 3) 个备份") {
                        showAllBackups.toggle()
                    }
                    .controlSize(.small)
                }
            }
        }
    }

    private func backupRow(_ backup: CallModeUSBBackupSummary) -> some View {
        let state = backupAvailability(backup)

        return VStack(alignment: .leading, spacing: 7) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(backupReasonText(backup))
                        .font(.callout.weight(.medium))
                    Text(backup.savedAt.formatted(date: .numeric, time: .shortened))
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
                Spacer(minLength: 8)
                Label(state.text, systemImage: state.icon)
                    .font(.caption)
                    .foregroundStyle(state.color)
            }

            backupIdentityLine(backup)

            backupActions(backup)
                .frame(maxWidth: .infinity, alignment: .trailing)

            if let detail = backup.detail, !detail.isEmpty {
                Text(detail)
                    .font(.caption)
                    .foregroundStyle(backup.valid ? Color.secondary : Color.red)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.vertical, 8)
        .help(backupTechnicalDetail(backup))
        .accessibilityElement(children: .contain)
    }

    private func backupIdentityLine(_ backup: CallModeUSBBackupSummary) -> some View {
        Text("IMEI 尾号 \(backupIMEISuffix(backup)) · ADB \(backup.adbEnabled ? "开启" : "关闭") · UAC \(backup.uacEnabled ? "开启" : "关闭")")
            .font(.caption)
            .foregroundStyle(.secondary)
            .lineLimit(1)
            .truncationMode(.middle)
    }

    private func backupActions(_ backup: CallModeUSBBackupSummary) -> some View {
        HStack(spacing: 8) {
            if store.callModeBackupExportingID == backup.id {
                ProgressView()
                    .controlSize(.small)
                    .accessibilityLabel("正在导出备份")
            } else {
                Button {
                    chooseExportLocation(for: backup)
                } label: {
                    Label("导出…", systemImage: "square.and.arrow.up")
                }
                .controlSize(.small)
                .disabled(
                    store.callModeBackupExportingID != nil
                        || store.callModeBackupImporting
                        || store.callModeBackupDeletingID != nil)
            }

            Button("还原…") {
                backupToRestore = backup
                showRestoreConfirmation = true
            }
            .controlSize(.small)
            .disabled(!canRestore(backup))
            .help(restoreHelp(backup))

            if store.callModeBackupDeletingID == backup.id {
                ProgressView()
                    .controlSize(.small)
                    .accessibilityLabel("正在删除配置备份")
            } else {
                Button(role: .destructive) {
                    backupToDelete = backup
                    showDeleteConfirmation = true
                } label: {
                    Label("删除…", systemImage: "trash")
                }
                .controlSize(.small)
                .disabled(!canDeleteBackup)
                .help("从本机删除这份配置备份")
            }
        }
    }

    private var callStateBadge: some View {
        Text(callStateText)
            .font(.caption.bold())
            .foregroundStyle(callStateColor)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(Capsule().fill(callStateColor.opacity(0.13)))
            .accessibilityLabel("通话状态：\(callStateText)")
    }

    private var callPanelTitle: String {
        store.callStatus.isIdle ? "拨打电话" : "当前通话"
    }

    private var callStateText: String {
        switch store.callStatus.state {
        case "active": return "通话中"
        case "incoming": return "来电"
        case "dialing": return "正在拨号"
        case "alerting": return "等待接听"
        case "held": return "通话保持中"
        case "unknown": return "状态未知"
        default: return "空闲"
        }
    }

    private var callStateColor: Color {
        switch store.callStatus.state {
        case "active": return .green
        case "incoming": return .red
        case "dialing", "alerting": return .orange
        case "unknown": return .orange
        default: return .secondary
        }
    }

    private var callError: String? {
        store.voiceError ?? store.audioError
    }

    private var pageStatusText: String {
        mode?.summary ?? "正在检查通话模式"
    }

    private var modeBadgeIcon: String {
        switch mode?.state {
        case "ready": return "checkmark.circle.fill"
        case "failed", "unsupported": return "exclamationmark.triangle.fill"
        case "disconnected": return "cable.connector.slash"
        case "checking", "authorizing_adb", "enabling_usb", "enabling_voice", "restarting", "verifying_usb", "downloading", "preparing", "restoring_usb", "restarting_restore":
            return "arrow.triangle.2.circlepath"
        default: return "circle.dashed"
        }
    }

    private var modeBadgeColor: Color {
        switch mode?.state {
        case "ready": return .green
        case "failed", "unsupported": return .red
        case "checking", "authorizing_adb", "enabling_usb", "enabling_voice", "restarting", "verifying_usb", "downloading", "preparing", "restoring_usb", "restarting_restore":
            return .orange
        default: return .secondary
        }
    }

    private var interfaceStepState: ReadinessState {
        if mode?.interfacesReady == true { return .complete }
        if mode?.state == "failed" { return .failed }
        if ["needs_adb_authorization", "needs_usb", "needs_voice", "needs_interface_recovery"].contains(mode?.state ?? "") { return .actionRequired }
        if ["authorizing_adb", "enabling_usb", "enabling_voice", "restarting", "verifying_usb", "restoring_usb", "restarting_restore"].contains(mode?.state ?? "") {
            return .current
        }
        return .pending
    }

    private var runtimeStepState: ReadinessState {
        if mode?.runtimeDownloaded == true { return .complete }
        if mode?.state == "failed" && (mode?.downloadedBytes ?? 0) > 0 { return .failed }
        if mode?.state == "needs_download" { return .actionRequired }
        if mode?.state == "downloading" { return .current }
        return .pending
    }

    private var deploymentStepState: ReadinessState {
        if mode?.isReady == true { return .complete }
        if mode?.state == "failed" && mode?.runtimeDownloaded == true { return .failed }
        if mode?.state == "preparing" { return .current }
        return .pending
    }

    private var interfaceDetail: String {
        switch mode?.state {
        case "authorizing_adb":
            return "正在本机计算并提交 ADB 授权"
        case "enabling_usb":
            return "正在保留原配置并开启缺少的接口"
        case "enabling_voice":
            return "正在启用并回读 IMS/VoLTE"
        case "restarting":
            return "配置已提交，模块正在重新枚举"
        case "verifying_usb":
            return "正在核对 IMEI、IMS/VoLTE 与 ADB root"
        case "needs_interface_recovery":
            return "ADB 已配置但未响应，需要受控重启恢复"
        default:
            break
        }
        if mode?.adbAuthorizationRequired == true {
            return "ADB 待持久授权 · UAC \(mode?.uacEnabled == true ? "已开启" : "未开启")"
        }
        return "ADB \(mode?.adbEnabled == true ? "已开启" : "未开启") · UAC \(mode?.uacEnabled == true ? "已开启" : "未开启") · IMS/VoLTE \(mode?.voiceConfigured == true ? "已就绪" : "未就绪")"
    }

    private var runtimeDetail: String {
        if mode?.runtimeDownloaded == true { return "固定版本已下载并通过校验" }
        if mode?.state == "downloading", let mode { return byteProgress(mode) }
        return "由用户选择渠道下载并校验"
    }

    private var deploymentDetail: String {
        switch mode?.state {
        case "preparing": return "正在部署驱动并执行模块自检"
        case "ready": return "驱动、校准与 D4/UAC 路由启停已验证"
        case "failed" where mode?.runtimeDownloaded == true: return "部署或自检未通过，可重试"
        default: return "下载完成后自动部署到模块"
        }
    }

    private var hasPreparationDetail: Bool {
        mode?.state == "downloading"
            || mode?.state == "needs_download"
            || !(store.callModeError ?? "").isEmpty
            || !(mode?.runtimePath ?? "").isEmpty
    }

    private var selectedDownloadSource: CallModeDownloadSource? {
        mode?.sources.first(where: { $0.id == downloadSource })
    }

    private var preparationLeads: Bool {
        mode?.isReady != true
    }

    private var visibleBackups: [CallModeUSBBackupSummary] {
        showAllBackups ? store.callModeBackups : Array(store.callModeBackups.prefix(3))
    }

    private var canDial: Bool {
        callReady
            && store.callStatus.isIdle
            && !store.dialNumber.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private var dialHelp: String {
        if !callReady { return "请先完成通话模式准备" }
        if !store.callStatus.isIdle { return "请先结束当前呼叫" }
        if store.dialNumber.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return "请输入电话号码" }
        return "拨打当前号码"
    }

    private func dial() {
        guard canDial else { return }
        store.dial(store.dialNumber)
    }

    private func appendPlus() {
        guard !store.dialNumber.contains("+") else { return }
        store.dialNumber.insert("+", at: store.dialNumber.startIndex)
    }

    private func canRedial(_ record: CallRecord) -> Bool {
        guard callReady, store.callStatus.isIdle else { return false }
        return !(record.callbackNumber?.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ?? true)
    }

    private func redial(_ record: CallRecord) {
        guard canRedial(record), let number = record.callbackNumber else { return }
        store.dialNumber = number
        store.dial(number)
    }

    private func redialHelp(_ record: CallRecord) -> String {
        guard !(record.callbackNumber?.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ?? true) else {
            return "这条记录没有可拨打的号码"
        }
        if !callReady { return "请先完成通话模式准备" }
        if !store.callStatus.isIdle { return "请先结束当前呼叫" }
        return "直接回拨"
    }

    private func recentCallIcon(_ record: CallRecord) -> String {
        if record.isMissed { return "phone.arrow.down.left.fill" }
        return record.isIncoming ? "phone.arrow.down.left" : "phone.arrow.up.right"
    }

    private func recentCallColor(_ record: CallRecord) -> Color {
        if record.isMissed { return .red }
        return record.isIncoming ? .green : .secondary
    }

    private func recentCallKind(_ record: CallRecord) -> String {
        if record.isMissed { return "未接" }
        return record.isIncoming ? "呼入" : "呼出"
    }

    private func recentCallMetadata(_ record: CallRecord) -> String {
        let date: String
        if Calendar.current.isDateInToday(record.startedAt) {
            date = "今天 \(record.startedAt.formatted(date: .omitted, time: .shortened))"
        } else if Calendar.current.isDateInYesterday(record.startedAt) {
            date = "昨天 \(record.startedAt.formatted(date: .omitted, time: .shortened))"
        } else {
            date = record.startedAt.formatted(date: .abbreviated, time: .shortened)
        }

        guard record.duration > 0 else { return date }
        return "\(date) · \(durationText(record.duration))"
    }

    private func durationText(_ duration: Int) -> String {
        let minutes = duration / 60
        let seconds = duration % 60
        return minutes > 0 ? "\(minutes)分\(seconds)秒" : "\(seconds)秒"
    }

    private func backupReasonText(_ backup: CallModeUSBBackupSummary) -> String {
        if backup.imported { return "导入的模块配置" }
        switch backup.reason {
        case "before_call_mode": return "启用通话模式前"
        case "before_restore": return "还原前自动保护"
        default: return backup.schemaVersion == 0 ? "旧版配置备份" : "模块配置备份"
        }
    }

    private func backupIMEISuffix(_ backup: CallModeUSBBackupSummary) -> String {
        guard let imei = backup.moduleIMEI, imei.count >= 4 else { return "未知" }
        return String(imei.suffix(4))
    }

    private func backupAvailability(_ backup: CallModeUSBBackupSummary) -> (text: String, icon: String, color: Color) {
        if !backup.valid {
            return ("备份无效", "exclamationmark.triangle.fill", .red)
        }
        if !backup.restorable {
            return ("仅可导出", "square.and.arrow.down", .orange)
        }
        if mode?.state == "disconnected" || store.statusStale {
            return ("模块未连接", "cable.connector.slash", .secondary)
        }
        guard let currentIMEI = store.status?.imei, !currentIMEI.isEmpty else {
            return ("等待模块身份", "ellipsis.circle", .secondary)
        }
        guard currentIMEI == backup.moduleIMEI else {
            return ("其他模块", "person.crop.circle.badge.exclamationmark", .orange)
        }
        return ("当前模块", "checkmark.circle.fill", .green)
    }

    private func canRestore(_ backup: CallModeUSBBackupSummary) -> Bool {
        backup.valid
            && backup.restorable
            && backup.moduleIMEI == store.status?.imei
            && mode?.state != "disconnected"
            && !store.statusStale
            && store.callStatus.isIdle
            && !store.callModeActionInFlight
    }

    private var canDeleteBackup: Bool {
        !store.callModeActionInFlight
            && !store.callModeBackupImporting
            && store.callModeBackupExportingID == nil
            && store.callModeBackupDeletingID == nil
    }

    private func restoreHelp(_ backup: CallModeUSBBackupSummary) -> String {
        if !backup.valid { return "备份内容无效，不能还原" }
        if !backup.restorable { return "旧版备份缺少模块身份，只能导出" }
        if mode?.state == "disconnected" || store.statusStale { return "请先连接模块并等待状态刷新" }
        guard let currentIMEI = store.status?.imei, !currentIMEI.isEmpty else { return "正在等待模块身份" }
        if currentIMEI != backup.moduleIMEI { return "该备份属于另一台模块" }
        if !store.callStatus.isIdle { return "通话或呼叫进行中不能还原" }
        if store.callModeActionInFlight { return "请等待当前模块操作完成" }
        return "还原该备份并重启模块"
    }

    private func backupTechnicalDetail(_ backup: CallModeUSBBackupSummary) -> String {
        let identity = [backup.vendorID, backup.productID].compactMap { $0 }.joined(separator: ":")
        let flags = backup.flags?.map(String.init).joined(separator: ",") ?? "未知"
        let firmware = backup.firmware.flatMap { $0.isEmpty ? nil : $0 } ?? "未知"
        let voice = backup.voiceIncluded
            ? "IMS/VoLTE：\(backup.imsConfigured ? "已启用" : "已记录原始状态")"
            : "IMS/VoLTE：旧版备份未包含"
        return "文件：\(backup.fileName)\nUSB：\(identity.isEmpty ? "未知" : identity)\n功能位：\(flags)\n\(voice)\n固件：\(firmware)"
    }

    private var restoreConfirmationMessage: String {
        guard let backup = backupToRestore else { return "" }
        let time = backup.savedAt.formatted(date: .numeric, time: .shortened)
        let voiceScope = backup.voiceIncluded ? "，并还原当时的 IMS/VoLTE 配置" : "；这是旧版备份，IMS/VoLTE 不会改变"
        return "将把当前连接的模块还原到 \(time) 保存的配置（ADB \(backup.adbEnabled ? "开启" : "关闭")，UAC \(backup.uacEnabled ? "开启" : "关闭")\(voiceScope)）。应用会先自动备份当前配置，再写入、完整回读并重启模块；网络、短信和 eSIM 操作会短暂中断。本地下载的语音运行时不会删除。"
    }

    private var deleteConfirmationMessage: String {
        guard let backup = backupToDelete else { return "" }
        let time = backup.savedAt.formatted(date: .numeric, time: .shortened)
        return "将从本机永久删除 \(time) 的配置备份。此操作不会修改当前模块，但删除后无法在 DJOneHub 中导出或还原这份备份。"
    }

    private var usbConfirmationTitle: String {
        if mode?.state == "needs_interface_recovery" {
            return "重启并恢复模块 ADB？"
        }
        return mode?.requiresADBAuthorizationConfirmation == true
            ? "持久授权模块 ADB？"
            : "开启通话模式？"
    }

    private var usbConfirmationActionTitle: String {
        if mode?.state == "needs_interface_recovery" {
            return "备份、重启并验证"
        }
        return mode?.requiresADBAuthorizationConfirmation == true
            ? "授权、开启并重启模块"
            : "开启并重启模块"
    }

    private var usbConfirmationMessage: String {
        if mode?.state == "needs_interface_recovery" {
            return "ADB/UAC 配置位已经开启，但真实 ADB root 通道没有响应。应用会先备份当前 USB 与 IMS/VoLTE 配置，再重新提交仅在本机计算的 ADB 授权密码；密码不会上传或保存。随后不扩大任何功能位，受控重启模块并重新验证同一 IMEI、完整配置和 ADB root。网络、短信和 eSIM 操作会中断约 20–60 秒。"
        }
        if mode?.requiresADBAuthorizationConfirmation == true {
            return "应用会先备份当前 USB 与 IMS/VoLTE 配置，再在本机计算授权密码并持久授权模块 ADB；密码不会上传或保存。随后只开启缺少的 ADB/UAC 功能位、启用 IMS/VoLTE 并重启模块。配置备份可以关闭 ADB 接口，但不能保证撤销持久授权。重启期间 4G 网络、短信和 eSIM 操作会中断约 20–60 秒。"
        }
        return "应用会先备份当前 USB 与 IMS/VoLTE 配置，只开启缺少的 ADB/UAC 功能位并启用 IMS/VoLTE，然后重启模块。重启后会验证同一模块、完整配置和真实 ADB root。网络、短信和 eSIM 操作会短暂中断，通常需要 20–60 秒恢复。"
    }

    private func chooseImportFile() {
        let panel = NSOpenPanel()
        panel.title = "导入模块配置备份"
        panel.message = "选择由 DJOneHub 导出的 JSON 备份。导入只保存到本机，不会立即修改模块。"
        panel.prompt = "导入"
        panel.allowedContentTypes = [.json]
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = false
        panel.canChooseFiles = true
        guard panel.runModal() == .OK, let source = panel.url else { return }
        store.importCallModeBackup(from: source)
    }

    private func chooseExportLocation(for backup: CallModeUSBBackupSummary) {
        let panel = NSSavePanel()
        panel.title = "导出模块配置备份"
        panel.message = "备份包含模块 IMEI、USB 与 IMS/VoLTE 配置，请妥善保存。"
        panel.prompt = "导出"
        panel.nameFieldStringValue = backup.fileName
        panel.allowedContentTypes = [.json]
        panel.canCreateDirectories = true
        panel.isExtensionHidden = false
        guard panel.runModal() == .OK, let destination = panel.url else { return }
        store.exportCallModeBackup(backup, to: destination)
    }

    private var downloadConfirmationMessage: String {
        let sourceName = selectedDownloadSource?.name ?? "所选渠道"
        let path = mode?.runtimePath ?? "Application Support/DJOneHubNative"
        return "将从\(sourceName)下载固定提交 \(moduleShortVersion)，保存到 \(path)。每个文件都会验证大小与 SHA-256；校验失败的内容不会落盘或部署。"
    }

    private var moduleShortVersion: String {
        guard let version = mode?.runtimeVersion, !version.isEmpty else { return "QDC507 3.18.44" }
        return version
    }

    private func byteProgress(_ mode: CallModeStatus) -> String {
        let formatter = ByteCountFormatter()
        formatter.countStyle = .file
        return "\(formatter.string(fromByteCount: mode.downloadedBytes)) / \(formatter.string(fromByteCount: mode.totalBytes))"
    }

    private func promptForDownloadIfNeeded(_ state: String?) {
        guard state == "needs_download", !hasPromptedForDownload else { return }
        hasPromptedForDownload = true
        DispatchQueue.main.async {
            showDownloadConfirmation = true
        }
    }

    private func revealPath(_ path: String) {
        let url = URL(fileURLWithPath: path, isDirectory: true)
        if FileManager.default.fileExists(atPath: path) {
            NSWorkspace.shared.activateFileViewerSelecting([url])
        } else {
            NSWorkspace.shared.open(url.deletingLastPathComponent())
        }
    }

    private func openURL(_ value: String) {
        guard let url = URL(string: value) else { return }
        NSWorkspace.shared.open(url)
    }
}

private struct DialKey {
    let symbol: String
    let letters: String
}

private enum CallLayoutMode: Equatable {
    case wide
    case compact
    case stacked

    static func resolve(width: CGFloat) -> CallLayoutMode {
        if width >= 900 { return .wide }
        if width >= 660 { return .compact }
        return .stacked
    }
}

private struct CallLayoutModePreferenceKey: PreferenceKey {
    static var defaultValue = CallLayoutMode.compact

    static func reduce(value: inout CallLayoutMode, nextValue: () -> CallLayoutMode) {
        value = nextValue()
    }
}

private enum ReadinessState: Equatable {
    case complete
    case actionRequired
    case current
    case pending
    case failed

    var accessibilityText: String {
        switch self {
        case .complete: return "已完成"
        case .actionRequired: return "待操作"
        case .current: return "当前步骤"
        case .pending: return "等待"
        case .failed: return "失败"
        }
    }
}
