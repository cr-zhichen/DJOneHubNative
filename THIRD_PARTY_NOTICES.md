# Third-Party Notices

DJOneHub contains code derived from the upstream VoHive project and retains the license and required notice provided in the repository root [`LICENSE`](LICENSE):

```text
Required Notice: Copyright iniwex5 (https://github.com/iniwex5/vohive)
```

The call-mode USB/ADB design was also informed by the PolyForm Noncommercial reference repository [`rogerbush007-a11y/DJOneHub-mac-enhanced`](https://github.com/rogerbush007-a11y/DJOneHub-mac-enhanced), inspected at revision `f6ee6aa5ba5d1ca13ef65956adda7bc098c2fb92`. It carries the same upstream required notice and is not embedded as a binary dependency.

Legacy `QADBKEY` interoperability was informed by the public GPL-3.0-licensed [`carp4/qadbkey-unlock`](https://github.com/carp4/qadbkey-unlock) reference at revision `cab52a0a7429c8d8b8f31da8894c8c93155c0fc5` and independently implemented in Go using the documented Unix MD5-crypt format. DJOneHub does not bundle or execute that Python script; the compatibility implementation derives the password locally in memory and never persists it.

## Release Runtime

The macOS release package includes **libusb 1.0.30**, distributed under the GNU Lesser General Public License, version 2.1 or later.

- Project: <https://libusb.info/>
- Source: <https://github.com/libusb/libusb/releases/tag/v1.0.30>
- License text in the release package: `licenses/libusb-COPYING`

The macOS application bundle also includes an **unmodified, separately executed sing-box 1.13.16 network core**, distributed under the GNU General Public License, version 3 or later. DJOneHub communicates with it only through generated configuration and process control; it is not linked into the DJOneHub executable or Go backend.

- Project: <https://github.com/SagerNet/sing-box>
- Exact source revision: `17ec3c71af8ca946dc50bf0d927c39fc77322aec`
- Build recipe: `scripts/build-network-core.sh`
- License text in the application bundle: `Contents/Resources/backend/sing-box-GPL-3.0-or-later.txt`
- Corresponding source archive: attached to each DJOneHub GitHub Release as `sing-box-1.13.16-source.tar.gz`

## Optional Module Voice Runtime

DJOneHub does **not** include the QDC507 module voice runtime in its source tree or release package. When a user explicitly confirms the download in the call page, the application downloads the files from an immutable MaVo revision, either directly from GitHub or through a user-selected HTTPS relay, then verifies fixed file sizes and SHA-256 hashes before saving or deploying anything.

- Project: <https://github.com/moluncn/mavo>
- Exact source revision: `0443dfdaf8aec086fd76ba2ee9152fd908114524`
- Upstream runtime directory: <https://github.com/moluncn/mavo/tree/0443dfdaf8aec086fd76ba2ee9152fd908114524/Resources/ModuleVoice>
- Local download directory: `~/Library/Application Support/DJOneHubNative/voice-runtime/qdc507-3.18.44-voice-20260712.5`
- Downloaded license copy: `COPYING-GPL-2.0`
- Downloaded build/safety report: `MODULE-REPORT.md`

The downloaded helper `mavo-pcm-bridge.armv7` is part of the MIT-licensed MaVo project. The two downloaded kernel objects expose `MODULE_LICENSE("GPL v2")` metadata and are accompanied upstream by the GPLv2 text. DJOneHub neither mirrors nor republishes these artifacts; relay services are transport alternatives and are not treated as trusted publishers. This notice describes the application behavior and does not replace the upstream license materials.

## Vendored Source Dependencies

The source repository includes vendored dependencies under `third_party/` so the versions used by DJOneHub remain reproducible. Their original copyright notices and license texts are retained in the corresponding directories.

| Component | License file |
| --- | --- |
| euicc-go | `third_party/euicc-go/LICENSE` |
| uicc-go | `third_party/uicc-go/LICENSE` |
| quectel-qmi-go | `third_party/quectel-qmi-go/LICENSE` |
| strftime | `third_party/strftime/LICENSE` |
| pkg/errors | `third_party/pkg-errors/LICENSE` |
| golang.org/x/sys | `third_party/x-sys/LICENSE` |
| golang.org/x/text | `third_party/x-text/LICENSE` |
| multierr | `third_party/multierr/LICENSE.txt` |

Dependencies fetched through Go modules retain their own licenses and copyright notices. This file is informational and does not replace any component's full license text.
