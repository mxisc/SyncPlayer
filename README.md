# SyncPlayer

Windows 视频与 Funscript 播放器，支持本地播放、DeoVR Classic 远程播放，以及蓝牙和互联网设备同步。

## 功能

- 递归扫描媒体目录，搜索视频并自动匹配同名 Funscript。
- 脚本波形显示与时间偏移调节。
- Handy 蓝牙及互联网连接，腰带骑士 蓝牙连接。
- 向局域网中的 DeoVR Classic 提供媒体库，并跟随播放进度同步设备。
- 本地语音识别、中文字幕翻译与双语字幕。

## 开始使用

需要 Windows x64、WebView2 Runtime；使用蓝牙连接时，电脑需要可用的蓝牙适配器。

使用完整运行目录启动 `build/bin/SyncPlayer.exe`，保留 `build/bin` 与 `runtime` 的相对位置。源码仓库不包含播放器运行依赖和语音模型，源码构建请参见下文。

### 本地播放

1. 选择「本地播放」。
2. 在媒体库选择视频目录，或直接选择视频、音频文件。
3. 程序自动加载同目录、同名的 `.funscript`，也可以手动选择脚本。
4. 连接设备，可开启伸缩或震动，然后播放。

视频与脚本的命名示例：

```text
Movies/
  Example.mp4
  Example.funscript
  Example.png
```

封面优先使用视频同名图片，或所在目录的 `cover.jpg`、`folder.jpg` 等 JPG/PNG 图片。没有图片时，首次请求封面会提取视频首帧，并保存为同名 PNG。

### 连接设备

**蓝牙：** 将设备置于蓝牙模式，在「设备」中选择「蓝牙」，点击「扫描设备」，从列表选择设备后点击「连接设备」。连接前请断开其他软件对该设备的连接。

**Handy 互联网：** 先完成 Handy 的 Wi-Fi 配网，在「设备」中选择「互联网」，填写连接 Key，点击「连接 Handy」。成功连接的 Key 会保存在本机，后续可直接使用。

「时间偏移」用于调整脚本与画面的相对时间，正值让脚本提前。Handy 支持伸缩同步；FN010-RX 支持伸缩和震动，其伸缩采用速度与周期控制，不提供精确位置反馈。

### DeoVR Classic

1. 电脑和头显连接同一局域网。
2. 在 DeoVR Classic 中开启 **Enable remote control**。
3. SyncPlayer 选择「DeoVR」和「远程」，选择媒体库目录。
4. 在头显的 DeoVR Classic 中打开界面显示的媒体库地址，选择视频播放。
5. 在 SyncPlayer 连接设备，程序会自动连接请求视频的头显，并根据播放进度同步同名脚本。

若使用电脑上运行的 DeoVR，则选择「本地」。字幕翻译仅用于 SyncPlayer 本地播放。

### 字幕翻译

在本地播放模式的「字幕翻译」中选择服务商，填写 API Key，获取并选择模型后开启中文字幕；需要时可开启双语字幕。

语音识别在本机运行，识别后的文字发送至所选翻译服务。支持预置服务商和自定义兼容接口，调用费用由对应服务商收取。

## 从源码构建

需要 Go（版本要求见 `go.mod`）、Node.js、npm，以及 Windows WebView2 Runtime。

先按 [运行依赖说明](NATIVE-DEPENDENCIES.md) 准备 MPV、FFmpeg、Whisper.cpp 和语音模型，然后在项目根目录运行：

```powershell
./build.ps1
```

生成的程序位于 `build/bin/SyncPlayer.exe`。需要整理为可分发目录时运行：

```powershell
./package.ps1
```

输出位于 `dist/`，包含程序及运行依赖，不包含个人设置、密钥、日志或字幕缓存。
