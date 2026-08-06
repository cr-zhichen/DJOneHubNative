# Third-Party Notices

DJOneHub contains code derived from the upstream VoHive project and retains the license and required notice provided in the repository root [`LICENSE`](LICENSE):

```text
Required Notice: Copyright iniwex5 (https://github.com/iniwex5/vohive)
```

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
