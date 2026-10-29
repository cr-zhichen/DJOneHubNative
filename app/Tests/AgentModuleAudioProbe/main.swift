import Foundation

let devices = AgentModuleCoreAudioDiscovery.list()
for device in devices {
    print("device name=\(device.name) uid_hash=\(device.uidHash) input_channels=\(device.inputChannels) output_channels=\(device.outputChannels) sample_rate=\(device.sampleRate) bits=\(device.bitsPerChannel) pcm=\(device.isPCM) likely_module=\(device.likelyModule)")
}
let modules = devices.filter(\.likelyModule)
if modules.isEmpty {
    print("module_audio_devices=0")
} else {
    print("module_audio_devices=\(modules.count)")
}
