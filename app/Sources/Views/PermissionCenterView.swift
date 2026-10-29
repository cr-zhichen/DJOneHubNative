import SwiftUI

struct PermissionCenterSection: View {
    @StateObject private var center = SystemPermissionCenter()

    var body: some View {
        Section("系统权限") {
            HStack(spacing: 8) {
                Label(overallTitle, systemImage: overallIcon)
                    .foregroundStyle(overallColor)
                Spacer()
                Button {
                    center.refresh()
                } label: {
                    if center.isRefreshing {
                        ProgressView()
                            .controlSize(.small)
                    } else {
                        Label("刷新", systemImage: "arrow.clockwise")
                    }
                }
                .disabled(center.isRefreshing || center.requestingID != nil)
                .accessibilityLabel("刷新系统权限")
            }

            Text("系统权限与 AI CLI 授权相互独立。系统授权只开放对应的 macOS 能力，不会自动扩大下方 CLI 权限范围。")
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            ForEach(center.snapshot.items) { item in
                permissionRow(item)
            }
        }
        .onAppear { center.refresh() }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            center.refresh()
        }
    }

    private func permissionRow(_ item: PermissionCenterItem) -> some View {
        HStack(alignment: .center, spacing: 10) {
            Image(systemName: item.systemImage)
                .frame(width: 20)
                .foregroundStyle(item.presentation.level == .warning ? Color.orange : Color.secondary)
                .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(item.title)
                    Text(item.importance == .required ? "核心" : "按需")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                Text(item.detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            Spacer(minLength: 12)

            Text(item.presentation.status)
                .font(.caption.weight(.medium))
                .foregroundStyle(color(for: item.presentation.level))

            if center.requestingID == item.id {
                ProgressView()
                    .controlSize(.small)
                    .accessibilityLabel("正在请求\(item.title)权限")
            } else {
                actionButton(item)
            }
        }
        .padding(.vertical, 2)
    }

    @ViewBuilder
    private func actionButton(_ item: PermissionCenterItem) -> some View {
        switch item.presentation.action {
        case .request:
            Button("授权") { center.perform(item) }
                .controlSize(.small)
                .disabled(center.requestingID != nil || center.isRefreshing)
                .accessibilityLabel("授权\(item.title)权限")
        case .openSettings:
            Button("系统设置") { center.perform(item) }
                .controlSize(.small)
                .accessibilityLabel("打开\(item.title)系统设置")
        case .none:
            EmptyView()
        }
    }

    private var overallTitle: String {
        switch center.snapshot.overallLevel {
        case .healthy: return "核心权限可用"
        case .warning: return "有核心权限需要处理"
        case .neutral: return "权限将在使用时按需申请"
        }
    }

    private var overallIcon: String {
        switch center.snapshot.overallLevel {
        case .healthy: return "checkmark.shield.fill"
        case .warning: return "exclamationmark.shield.fill"
        case .neutral: return "shield"
        }
    }

    private var overallColor: Color {
        color(for: center.snapshot.overallLevel)
    }

    private func color(for level: PermissionCenterLevel) -> Color {
        switch level {
        case .healthy: return .green
        case .warning: return .orange
        case .neutral: return .secondary
        }
    }
}
