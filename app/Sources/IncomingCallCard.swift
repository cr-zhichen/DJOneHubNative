import AppKit
import SwiftUI

/// 自定义来电通知卡片：无边框浮层面板，替代系统通知。
/// 来电时固定在主屏幕右上角（最上层、不可拖动），不自动消失；
/// 挂断、通话结束或点击卡片后收起。
@MainActor
final class IncomingCallCard {
    static let shared = IncomingCallCard()

    private var panel: NSPanel?
    private var controller: NSHostingController<IncomingCallCardContent>?
    private var ringSound: NSSound?
    private var ringTask: Task<Void, Never>?
    private var store: DashboardStore?
    private var preview = false
    /// 预览模式 30 秒自动收起任务（手动关闭时需注销，避免残留任务提前收起新卡片）
    private var previewHideTask: Task<Void, Never>?

    /// 卡片上显示的号码
    var number: String = "未知号码"

    private init() {}

    /// 弹出来电卡片并开始响铃。
    /// - Parameter store: 真实来电时传入（挂断走 store）；preview 模拟时传 nil
    func show(store: DashboardStore?, preview: Bool = false) {
        self.store = store
        self.preview = preview
        self.number = store?.callStatus.number ?? "+8613800000000"
        // 注销上一次的自动收起任务（手动关闭后再触发时不会提前收起）
        previewHideTask?.cancel()
        previewHideTask = nil

        if panel == nil {
            let content = IncomingCallCardContent(card: self)
            let controller = NSHostingController(rootView: content)
            let panel = NSPanel(contentViewController: controller)
            panel.styleMask = [.nonactivatingPanel, .borderless]
            panel.isFloatingPanel = true
            panel.level = .statusBar
            panel.hidesOnDeactivate = false
            panel.isOpaque = false
            panel.backgroundColor = .clear
            // 圆角阴影由卡片自身的 SwiftUI shadow 绘制，关闭面板矩形阴影
            panel.hasShadow = false
            panel.invalidateShadow()
            panel.isMovable = false
            panel.animationBehavior = .utilityWindow
            panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
            self.panel = panel
            self.controller = controller
        }
        guard let panel else { return }
        // 固定在主屏幕右上角（菜单栏下方）
        if let screen = NSScreen.main {
            let origin = NSPoint(
                x: screen.visibleFrame.maxX - panel.frame.width - 4,
                y: screen.visibleFrame.maxY - panel.frame.height - 4)
            panel.setFrameOrigin(origin)
        }
        panel.orderFrontRegardless()
        startRing()

        if preview {
            // 模拟卡片 30 秒后自动收起
            previewHideTask = Task { [weak self] in
                try? await Task.sleep(nanoseconds: 30_000_000_000)
                guard !Task.isCancelled else { return }
                self?.hide()
            }
        }
    }

    /// 收起卡片并停止响铃（不挂断）
    func hide() {
        stopRing()
        previewHideTask?.cancel()
        previewHideTask = nil
        preview = false
        panel?.orderOut(nil)
    }

    /// 挂断并收起
    func hangup() {
        if let store, !preview {
            store.hangup()
        }
        hide()
    }

    /// 点击卡片/查看详情：拉起主窗口并弹出通话详情（预览模式也走同一路径）
    func openDetail() {
        if AppRuntimeConfiguration.usesModernSceneLifecycle {
            MainWindowRequestCenter.shared.requestOpen()
        } else {
            NSApp.activate(ignoringOtherApps: true)
            // 主窗口最小化时先还原
            if let window = NSApp.windows.first(where: { $0.isMiniaturized }) {
                window.deminiaturize(nil)
            }
        }
        if let store {
            store.showCallDetail = true
        }
        hide()
    }

    // MARK: - 响铃

    /// 铃声播放间隔：完整播完一段后停顿再响，像真实来电
    private static let ringPause: TimeInterval = 2.5

    private func startRing() {
        stopRing()
        scheduleRing()
    }

    private func scheduleRing() {
        guard let sound = loadRingSound() else { return }
        ringSound = sound
        sound.play()
        let interval = sound.duration + Self.ringPause
        ringTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: UInt64(interval * 1_000_000_000))
            guard !Task.isCancelled else { return }
            self?.scheduleRing()
        }
    }

    private func stopRing() {
        ringTask?.cancel()
        ringTask = nil
        ringSound?.stop()
        ringSound = nil
    }

    /// 播放用户选中的铃声；静音时不播放；缺失时回退到默认铃声，再缺失回退系统音效
    private func loadRingSound() -> NSSound? {
        let id = Ringtones.selectedID()
        guard id != Ringtones.silentID else { return nil }
        if let url = Ringtones.soundURL(for: id) {
            return NSSound(contentsOf: url, byReference: true)
        }
        if let url = Ringtones.soundURL(for: Ringtones.defaultID) {
            return NSSound(contentsOf: url, byReference: true)
        }
        return NSSound(named: "Glass")
    }
}

/// 卡片内容
struct IncomingCallCardContent: View {
    let card: IncomingCallCard

    var body: some View {
        VStack(spacing: 10) {
            HStack {
                Label("来电", systemImage: "phone.fill")
                    .font(.callout.bold())
                    .foregroundStyle(.red)
                Spacer()
                Button {
                    card.hide()
                } label: {
                    Image(systemName: "xmark")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
                .help("收起卡片（不挂断）")
            }

            // 点击信息区域打开通话详情
            VStack(alignment: .leading, spacing: 6) {
                Text(card.number)
                    .font(.title2.bold())
                    .textSelection(.enabled)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
            .onTapGesture {
                card.openDetail()
            }

            Divider()

            HStack(spacing: 8) {
                Button {
                    card.hangup()
                } label: {
                    Label("挂断", systemImage: "phone.down.fill")
                }
                .buttonStyle(RedActionButtonStyle())

                Spacer()

                Button("查看详情") {
                    card.openDetail()
                }
                .buttonStyle(.bordered)
            }
        }
        .padding(14)
        .frame(width: 300)
        .background(
            RoundedRectangle(cornerRadius: 14)
                .fill(Color(nsColor: .windowBackgroundColor))
                .shadow(color: .black.opacity(0.12), radius: 4, y: 1)
        )
        .overlay(
            RoundedRectangle(cornerRadius: 14)
                .stroke(Color(nsColor: .separatorColor), lineWidth: 1)
        )
        // 透明外边距：给阴影留出扩散空间，避免被窗口边缘裁剪成直角
        .padding(8)
    }
}

/// 红色主按钮样式（面板内 .tint 可能不生效，直接指定红色背景）
struct RedActionButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.callout.weight(.medium))
            .padding(.horizontal, 12)
            .padding(.vertical, 5)
            .background(
                RoundedRectangle(cornerRadius: 6)
                    .fill(Color.red.opacity(configuration.isPressed ? 0.75 : 1.0))
            )
            .foregroundStyle(.white)
    }
}
