# 运行依赖

SyncPlayer 使用以下原生组件。源码仓库不包含这些二进制文件和模型，构建及打包前需放入对应目录。

| 组件 | 目录与主要文件 | 获取来源 |
| --- | --- | --- |
| MPV | `runtime/mpv/mpv.exe` | [Windows 构建](https://github.com/shinchiro/mpv-winbuild-cmake/releases)；也可运行 `runtime/install-mpv.ps1`（需要 7-Zip） |
| FFmpeg / ffprobe | `runtime/ffmpeg/ffmpeg.exe`、`ffprobe.exe` | [FFmpeg 下载](https://ffmpeg.org/download.html)、[Gyan Windows 构建](https://www.gyan.dev/ffmpeg/builds/) |
| Whisper.cpp | `runtime/whisper/whisper-cli.exe` 及同包 CPU DLL | [v1.9.2 Windows CPU x64](https://github.com/ggml-org/whisper.cpp/releases/tag/v1.9.2) |
| Whisper small 模型 | `runtime/models/ggml-small.bin` | [模型下载](https://huggingface.co/ggerganov/whisper.cpp/blob/main/ggml-small.bin) |

模型 SHA256：`1be3a9b2063867b937e64e2ec7483364a79917e157fa98c5d94b5c1fffea987b`。

Whisper.cpp 的 CPU 后端 DLL 应与 `whisper-cli.exe` 放在同一目录。将模型许可证保存为 `runtime/models/LICENSE-whisper.txt`，以供打包使用。

分发时保留各组件随附的许可证和版权说明。Whisper.cpp 使用 MIT 许可证；模型许可证见 [Whisper LICENSE](https://github.com/openai/whisper/blob/main/LICENSE)。FFmpeg 与 MPV 按所使用构建的许可证要求分发。

运行程序不需要安装 Go、Node.js 或 Python。Windows WebView2 和组件所需的 Microsoft Visual C++ Runtime 需在目标电脑上安装。
