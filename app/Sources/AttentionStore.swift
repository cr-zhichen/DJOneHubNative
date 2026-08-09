import AppKit
import Foundation
import SwiftUI

/// 应用级待处理提醒：统一维护未读短信与未查看来电，并持久化到本机。
@MainActor
final class AttentionStore: ObservableObject {
    static let shared = AttentionStore()

    @Published private(set) var unreadSMSCount: Int
    @Published private(set) var unviewedCallCount: Int
    private(set) var viewingCalls = false

    private static let unreadSMSKey = "attentionUnreadSMSCount"
    private static let unviewedCallKey = "attentionUnviewedCallCount"
    private static let callSeenIDsKey = "attentionSeenCallIDs"
    private static let callHistorySeededKey = "attentionCallHistorySeeded"
    private static let maximumStoredCount = 9_999
    private static let maximumSeenCallIDs = 2_000

    private let defaults: UserDefaults
    private var seenCallIDs: Set<String>
    private var seenCallOrder: [String]
    private var callHistorySeeded: Bool

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        unreadSMSCount = max(0, defaults.integer(forKey: Self.unreadSMSKey))
        unviewedCallCount = max(0, defaults.integer(forKey: Self.unviewedCallKey))
        seenCallOrder = defaults.stringArray(forKey: Self.callSeenIDsKey) ?? []
        seenCallIDs = Set(seenCallOrder)
        callHistorySeeded = defaults.bool(forKey: Self.callHistorySeededKey)
            || !seenCallOrder.isEmpty
    }

    var totalCount: Int {
        unreadSMSCount + unviewedCallCount
    }

    var hasAttention: Bool {
        totalCount > 0
    }

    var menuBarSummary: String? {
        var parts: [String] = []
        if unreadSMSCount > 0 {
            parts.append("短信 \(Self.compactCount(unreadSMSCount))")
        }
        if unviewedCallCount > 0 {
            parts.append("电话 \(Self.compactCount(unviewedCallCount))")
        }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    var accessibilitySummary: String? {
        guard hasAttention else { return nil }
        return "未读短信 \(unreadSMSCount) 条，未查看电话 \(unviewedCallCount) 个"
    }

    func recordIncomingSMS(count: Int, isVisible: Bool) {
        guard count > 0, !isVisible else { return }
        unreadSMSCount = limited(unreadSMSCount + count)
        persistCounts()
    }

    func reconcileCallHistory(_ records: [CallRecord]) {
        // 与系统“电话”徽标一致：只把用户未处理的未接来电计入提醒，
        // 已接听和主动拨出的记录不制造新的待处理数量。
        let missedCallIDs = records.filter(\.isMissed).map(\.id)
        let newIDs = missedCallIDs.filter { !seenCallIDs.contains($0) }
        rememberCallIDs(missedCallIDs)

        guard callHistorySeeded else {
            callHistorySeeded = true
            defaults.set(true, forKey: Self.callHistorySeededKey)
            return
        }
        guard !newIDs.isEmpty, !(viewingCalls && NSApp.isActive) else { return }
        unviewedCallCount = limited(unviewedCallCount + newIDs.count)
        persistCounts()
    }

    func markSMSViewed() {
        guard unreadSMSCount != 0 else { return }
        unreadSMSCount = 0
        persistCounts()
    }

    func markCallsViewed() {
        guard unviewedCallCount != 0 else { return }
        unviewedCallCount = 0
        persistCounts()
    }

    func setViewingCalls(_ viewing: Bool) {
        viewingCalls = viewing
        if viewing {
            markCallsViewed()
        }
    }

    func incrementUnreadSMSForDebug() {
        unreadSMSCount = limited(unreadSMSCount + 1)
        persistCounts()
    }

    func incrementUnviewedCallForDebug() {
        unviewedCallCount = limited(unviewedCallCount + 1)
        persistCounts()
    }

    func clearForDebug() {
        guard hasAttention else { return }
        unreadSMSCount = 0
        unviewedCallCount = 0
        persistCounts()
    }

    static func compactCount(_ count: Int) -> String {
        count > 99 ? "99+" : String(max(0, count))
    }

    private func limited(_ value: Int) -> Int {
        min(Self.maximumStoredCount, max(0, value))
    }

    private func persistCounts() {
        defaults.set(unreadSMSCount, forKey: Self.unreadSMSKey)
        defaults.set(unviewedCallCount, forKey: Self.unviewedCallKey)
    }

    private func rememberCallIDs(_ ids: [String]) {
        for id in ids where !seenCallIDs.contains(id) {
            seenCallIDs.insert(id)
            seenCallOrder.append(id)
        }
        if seenCallOrder.count > Self.maximumSeenCallIDs {
            let overflow = seenCallOrder.count - Self.maximumSeenCallIDs
            for id in seenCallOrder.prefix(overflow) {
                seenCallIDs.remove(id)
            }
            seenCallOrder.removeFirst(overflow)
        }
        defaults.set(seenCallOrder, forKey: Self.callSeenIDsKey)
    }
}

/// 与 macOS 通知徽标一致的小型计数气泡。
struct AttentionBadge: View {
    let count: Int
    var accessibilityName = "未读项目"

    var body: some View {
        Text(AttentionStore.compactCount(count))
            .font(.caption2.weight(.semibold).monospacedDigit())
            .foregroundStyle(.white)
            .padding(.horizontal, 5)
            .frame(minWidth: 16, minHeight: 16)
            .background(Capsule().fill(Color.red))
            .accessibilityLabel("\(accessibilityName) \(count) 个")
    }
}
