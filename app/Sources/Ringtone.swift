import AppKit
import Foundation

/// 内置铃声（来自 iOS 系统铃声，WAV 格式位于 app/Resources/Ringtones）
struct Ringtone: Identifiable, Equatable {
    /// 资源文件名（不含扩展名）
    let id: String
    let displayName: String
}

enum Ringtones {
    static let defaultID = "Reflected"
    /// 静音选项：不播放任何铃声
    static let silentID = "silent"

    static let all: [Ringtone] = [
        Ringtone(id: silentID, displayName: "静音"),
        Ringtone(id: "Reflected", displayName: "Reflected"),
        Ringtone(id: "Buoyant", displayName: "Buoyant"),
        Ringtone(id: "Dreamer", displayName: "Dreamer"),
        Ringtone(id: "LittleBird", displayName: "Little Bird"),
        Ringtone(id: "Pond", displayName: "Pond"),
        Ringtone(id: "Pop", displayName: "Pop"),
        Ringtone(id: "Surge", displayName: "Surge"),
    ]

    static let userDefaultsKey = "ringtoneID"

    /// 当前选中的铃声 id（默认 Reflected）
    static func selectedID() -> String {
        UserDefaults.standard.string(forKey: userDefaultsKey) ?? defaultID
    }

    static func setSelected(_ id: String) {
        UserDefaults.standard.set(id, forKey: userDefaultsKey)
    }

    /// 铃声资源 URL（找不到时返回 nil）
    static func soundURL(for id: String) -> URL? {
        Bundle.main.url(forResource: id, withExtension: "wav", subdirectory: "Ringtones")
    }
}

/// 铃声试听播放器：播放/暂停当前选中的铃声
@MainActor
final class RingtonePreview: NSObject, ObservableObject {
    @Published private(set) var isPlaying = false

    private var sound: NSSound?

    func toggle() {
        isPlaying ? stop() : play()
    }

    func play() {
        guard let url = Ringtones.soundURL(for: Ringtones.selectedID()) else { return }
        let sound = NSSound(contentsOf: url, byReference: true)
        sound?.delegate = self
        self.sound = sound
        sound?.play()
        isPlaying = true
    }

    func stop() {
        sound?.stop()
        sound = nil
        isPlaying = false
    }
}

extension RingtonePreview: NSSoundDelegate {
    nonisolated func sound(_ sound: NSSound, didFinishPlaying flag: Bool) {
        Task { @MainActor in
            if self.sound === sound {
                self.sound = nil
                self.isPlaying = false
            }
        }
    }
}
