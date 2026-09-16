import AppKit
import MapKit
import SwiftUI

/// 模块定位：显示 QDC507 GNSS 状态与最近一次有效坐标。
struct GPSView: View {
    @StateObject private var store = GPSStore()
    @State private var region = MKCoordinateRegion(
        center: CLLocationCoordinate2D(latitude: 0, longitude: 0),
        latitudinalMeters: 800,
        longitudinalMeters: 800)
    @State private var copied = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                pageHeader

                if let error = visibleError {
                    errorPanel(error)
                }

                if store.status == nil {
                    loadingPanel
                } else {
                    statusPanel
                    locationPanel
                    detailPanel
                }
            }
            .padding(18)
            .frame(maxWidth: 900)
            .frame(maxWidth: .infinity)
        }
        .scrollIndicators(.visible)
        .onAppear {
            store.beginPolling()
        }
        .onDisappear {
            store.endPolling()
        }
        .onChange(of: coordinateKey) { _ in
            centerMapOnCurrentFix()
        }
    }

    private var pageHeader: some View {
        HStack(alignment: .center, spacing: 16) {
            VStack(alignment: .leading, spacing: 4) {
                Text("模块定位")
                    .font(.title2.bold())
                Text("读取连接模块的 GNSS 坐标，仅供 DJOneHub 显示与使用。")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }

            Spacer(minLength: 12)

            Button {
                store.refresh()
            } label: {
                Label("刷新", systemImage: "arrow.clockwise")
            }
            .disabled(store.refreshInFlight || store.actionInFlight)
            .accessibilityHint("重新读取模块定位状态")

            if store.refreshInFlight || store.actionInFlight {
                ProgressView()
                    .controlSize(.small)
                    .accessibilityLabel(store.actionInFlight ? "正在切换定位状态" : "正在刷新定位状态")
            }

            Toggle("启用定位", isOn: Binding(
                get: { store.status?.enabled ?? false },
                set: { store.setEnabled($0) }))
                .toggleStyle(.switch)
                .disabled(!store.canChangeEnabled)
                .accessibilityHint("控制连接模块的 GNSS 引擎")
        }
    }

    private var loadingPanel: some View {
        panel {
            HStack(spacing: 12) {
                ProgressView()
                    .controlSize(.small)
                VStack(alignment: .leading, spacing: 3) {
                    Text("正在读取定位状态")
                        .font(.headline)
                    Text("正在确认模块是否支持 GNSS 以及当前开关状态。")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var statusPanel: some View {
        panel {
            HStack(alignment: .top, spacing: 12) {
                Image(systemName: statePresentation.icon)
                    .font(.title2)
                    .foregroundStyle(statePresentation.color)
                    .frame(width: 28)
                    .accessibilityHidden(true)

                VStack(alignment: .leading, spacing: 4) {
                    Text(statePresentation.title)
                        .font(.headline)
                    Text(statePresentation.detail)
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }

                Spacer(minLength: 12)

                if let checkedAt = store.status?.checkedAt {
                    VStack(alignment: .trailing, spacing: 3) {
                        Text("最近检查")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        Text(checkedAt.formatted(date: .omitted, time: .standard))
                            .font(.callout.monospacedDigit())
                    }
                }
            }
        }
    }

    private var locationPanel: some View {
        panel {
            VStack(alignment: .leading, spacing: 12) {
                HStack(alignment: .firstTextBaseline) {
                    Text("位置")
                        .font(.headline)
                    Spacer()
                    if let updatedAt = store.status?.updatedAt {
                        Text("更新于 \(updatedAt.formatted(date: .omitted, time: .standard))")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }

                if coordinate != nil {
                    Map(
                        coordinateRegion: $region,
                        interactionModes: [.pan, .zoom],
                        annotationItems: mapPoints
                    ) { point in
                        MapAnnotation(coordinate: point.coordinate) {
                            Image(systemName: "location.circle.fill")
                                .font(.title)
                                .symbolRenderingMode(.palette)
                                .foregroundStyle(Color.white, Color.accentColor)
                                .accessibilityHidden(true)
                        }
                    }
                    .frame(minHeight: 250)
                    .clipShape(RoundedRectangle(cornerRadius: 8))
                    .overlay(
                        RoundedRectangle(cornerRadius: 8)
                            .stroke(Color(nsColor: .separatorColor), lineWidth: 1))
                    .accessibilityLabel("模块定位地图")
                } else {
                    VStack(spacing: 10) {
                        Image(systemName: statePresentation.emptyIcon)
                            .font(.system(size: 34))
                            .foregroundStyle(.tertiary)
                            .accessibilityHidden(true)
                        Text(statePresentation.emptyTitle)
                            .font(.headline)
                        Text(statePresentation.emptyDetail)
                            .font(.callout)
                            .foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                    }
                    .frame(maxWidth: .infinity, minHeight: 230)
                    .background(
                        RoundedRectangle(cornerRadius: 8)
                            .fill(Color(nsColor: .windowBackgroundColor)))
                }

                HStack(spacing: 8) {
                    Button {
                        copyCoordinates()
                    } label: {
                        Label(copied ? "已复制" : "复制坐标", systemImage: copied ? "checkmark" : "doc.on.doc")
                    }
                    .disabled(coordinate == nil)

                    Button {
                        openInMaps()
                    } label: {
                        Label("在地图中打开", systemImage: "map")
                    }
                    .disabled(coordinate == nil)
                }
            }
        }
    }

    private var detailPanel: some View {
        panel {
            VStack(alignment: .leading, spacing: 10) {
                Text("定位数据")
                    .font(.headline)

                LazyVGrid(
                    columns: [GridItem(.flexible(), spacing: 24), GridItem(.flexible(), spacing: 24)],
                    alignment: .leading,
                    spacing: 9
                ) {
                    detailRow("纬度", coordinateValue(store.status?.latitude))
                    detailRow("经度", coordinateValue(store.status?.longitude))
                    detailRow("海拔", altitudeValue)
                    detailRow("卫星", satelliteValue)
                    detailRow("HDOP", hdopValue)
                    detailRow("定位类型", fixTypeValue)
                    detailRow("GNSS 时间", gnssTimeValue)
                }
            }
        }
    }

    private func errorPanel(_ message: String) -> some View {
        panel {
            HStack(alignment: .top, spacing: 10) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 3) {
                    Text("定位暂不可用")
                        .font(.headline)
                    Text(message)
                        .font(.callout)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer(minLength: 8)
                Button("重新读取") {
                    store.refresh()
                }
                .disabled(store.refreshInFlight || store.actionInFlight)
            }
        }
    }

    private func detailRow(_ label: String, _ value: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text(label)
                .font(.callout)
                .foregroundStyle(.secondary)
            Spacer(minLength: 8)
            Text(value)
                .font(.callout.monospacedDigit())
                .textSelection(.enabled)
        }
    }

    private func panel<Content: View>(@ViewBuilder content: () -> Content) -> some View {
        content()
            .padding(14)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(
                RoundedRectangle(cornerRadius: 10)
                    .fill(Color(nsColor: .controlBackgroundColor)))
            .overlay(
                RoundedRectangle(cornerRadius: 10)
                    .stroke(Color(nsColor: .separatorColor), lineWidth: 1))
    }

    private var coordinate: CLLocationCoordinate2D? {
        guard let latitude = store.status?.latitude,
              let longitude = store.status?.longitude,
              (-90...90).contains(latitude),
              (-180...180).contains(longitude) else { return nil }
        return CLLocationCoordinate2D(latitude: latitude, longitude: longitude)
    }

    private var coordinateKey: String? {
        guard let coordinate else { return nil }
        return "\(coordinate.latitude),\(coordinate.longitude)"
    }

    private var mapPoints: [GPSMapPoint] {
        coordinate.map { [GPSMapPoint(coordinate: $0)] } ?? []
    }

    private var visibleError: String? {
        if let message = store.errorMessage, !message.isEmpty {
            return message
        }
        guard let status = store.status,
              status.state == .error || status.state == .unavailable,
              let message = status.error,
              !message.isEmpty else { return nil }
        return message
    }

    private var statePresentation: GPSStatePresentation {
        switch effectiveState {
        case .disabled:
            return GPSStatePresentation(
                title: "定位已关闭",
                detail: "启用后由模块搜索卫星，DJOneHub 会按模块状态返回的间隔更新坐标。",
                icon: "location.slash",
                color: .secondary,
                emptyIcon: "location.slash",
                emptyTitle: "定位尚未启用",
                emptyDetail: "启用模块定位后，这里会显示最近一次有效坐标。")
        case .searching:
            return GPSStatePresentation(
                title: "正在搜索卫星",
                detail: "GNSS 已启动，尚未获得有效定位。首次定位可能需要一段时间。",
                icon: "antenna.radiowaves.left.and.right",
                color: .accentColor,
                emptyIcon: "antenna.radiowaves.left.and.right",
                emptyTitle: "等待卫星定位",
                emptyDetail: "请确认已连接 GNSS 天线，并尽量在室外无遮挡位置等待。")
        case .fixed:
            return GPSStatePresentation(
                title: "当前位置已锁定",
                detail: "坐标来自连接模块的 GNSS 接收器。",
                icon: "location.fill",
                color: .green,
                emptyIcon: "location.fill",
                emptyTitle: "当前位置已锁定",
                emptyDetail: "正在读取有效坐标。")
        case .stale:
            return GPSStatePresentation(
                title: "位置数据已过期",
                detail: "最近一次有效坐标已过期，请检查天线和卫星信号。",
                icon: "clock.badge.exclamationmark",
                color: .orange,
                emptyIcon: "clock.badge.exclamationmark",
                emptyTitle: "位置数据已过期",
                emptyDetail: "等待模块返回新的有效坐标。")
        case .unavailable:
            return GPSStatePresentation(
                title: "模块连接不可用",
                detail: "请确认模块已连接，然后重新读取定位状态。",
                icon: "cable.connector.slash",
                color: .orange,
                emptyIcon: "cable.connector.slash",
                emptyTitle: "模块连接不可用",
                emptyDetail: "请确认模块已连接，然后重新读取定位状态。")
        case .unsupported:
            return GPSStatePresentation(
                title: "当前模块不支持定位",
                detail: "模块没有响应受支持的 GNSS 状态指令。",
                icon: "nosign",
                color: .secondary,
                emptyIcon: "nosign",
                emptyTitle: "当前模块不支持定位",
                emptyDetail: "模块没有确认支持所需的 GNSS 指令。")
        case .error:
            return GPSStatePresentation(
                title: "定位读取失败",
                detail: "模块已连接，但当前定位数据无法读取。",
                icon: "exclamationmark.triangle.fill",
                color: .orange,
                emptyIcon: "exclamationmark.triangle.fill",
                emptyTitle: "暂时无法读取定位",
                emptyDetail: "模块已连接，但当前定位数据无法读取，可稍后重试。")
        case .checking, .unknown:
            return GPSStatePresentation(
                title: "正在读取定位状态",
                detail: "正在确认模块当前的 GNSS 状态。",
                icon: "location.magnifyingglass",
                color: .secondary,
                emptyIcon: "location.magnifyingglass",
                emptyTitle: "正在读取定位状态",
                emptyDetail: "正在确认模块是否支持 GNSS 以及当前开关状态。")
        }
    }

    private var effectiveState: GPSState {
        store.status?.state ?? .checking
    }

    private var altitudeValue: String {
        guard let altitude = store.status?.altitude else { return "—" }
        return String(format: "%.1f 米", locale: Locale(identifier: "en_US_POSIX"), altitude)
    }

    private var satelliteValue: String {
        guard let satellites = store.status?.satellites else { return "—" }
        return "\(satellites) 颗"
    }

    private var hdopValue: String {
        guard let hdop = store.status?.hdop else { return "—" }
        return String(format: "%.1f", locale: Locale(identifier: "en_US_POSIX"), hdop)
    }

    private var fixTypeValue: String {
        switch store.status?.fixType {
        case 2: return "2D"
        case 3: return "3D"
        case .some(let value): return "类型 \(value)"
        case nil: return "—"
        }
    }

    private var gnssTimeValue: String {
        guard let utc = store.status?.utc, !utc.isEmpty else { return "—" }
        guard let date = store.status?.date, date.count >= 6, utc.count >= 6 else {
            return "\(utc) UTC"
        }

        let day = date.prefix(2)
        let month = date.dropFirst(2).prefix(2)
        let year = date.dropFirst(4).prefix(2)
        let hour = utc.prefix(2)
        let minute = utc.dropFirst(2).prefix(2)
        let second = utc.dropFirst(4).prefix(2)
        return "20\(year)-\(month)-\(day) \(hour):\(minute):\(second) UTC"
    }

    private func coordinateValue(_ value: Double?) -> String {
        guard let value else { return "—" }
        return String(format: "%.6f°", locale: Locale(identifier: "en_US_POSIX"), value)
    }

    private func centerMapOnCurrentFix() {
        guard let coordinate else { return }
        region = MKCoordinateRegion(
            center: coordinate,
            latitudinalMeters: 800,
            longitudinalMeters: 800)
    }

    private func copyCoordinates() {
        guard let coordinate else { return }
        let text = String(
            format: "%.6f, %.6f",
            locale: Locale(identifier: "en_US_POSIX"),
            coordinate.latitude,
            coordinate.longitude)
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        copied = true
        Task { @MainActor in
            try? await Task.sleep(nanoseconds: 1_500_000_000)
            copied = false
        }
    }

    private func openInMaps() {
        guard let coordinate else { return }
        let value = String(
            format: "%.6f,%.6f",
            locale: Locale(identifier: "en_US_POSIX"),
            coordinate.latitude,
            coordinate.longitude)
        var components = URLComponents()
        components.scheme = "https"
        components.host = "maps.apple.com"
        components.path = "/"
        components.queryItems = [
            URLQueryItem(name: "ll", value: value),
            URLQueryItem(name: "q", value: "DJOneHub GPS"),
        ]
        if let url = components.url {
            NSWorkspace.shared.open(url)
        }
    }
}

private struct GPSMapPoint: Identifiable {
    let id = "module-location"
    let coordinate: CLLocationCoordinate2D
}

private struct GPSStatePresentation {
    let title: String
    let detail: String
    let icon: String
    let color: Color
    let emptyIcon: String
    let emptyTitle: String
    let emptyDetail: String
}
