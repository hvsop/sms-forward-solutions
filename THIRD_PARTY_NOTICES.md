# 第三方组件

本项目自有代码采用 MIT；第三方组件仍适用各自许可证，不会因本仓库 MIT 文件而被重新许可。

- Go 后端仅使用 Go 标准库；Go 工具链不随仓库分发。
- `server/shareweb/qrcode.js`：qrcode-generator 1.4.4，Kazuhiko Arase，MIT，完整许可见同目录 `qrcode.LICENSE` 和源文件头部。
- Gradle Wrapper：Gradle 8.13，Apache-2.0。脚本保留原声明；随仓库分发的 Wrapper JAR 官方 SHA-256 为 `81a82aaea5abcc8ff68b3dfcb58b3c3c429378efd98e7433460610fecd7ae45f`，Apache 许可见 `android/gradle/wrapper/LICENSE`。
- Android 编译/运行依赖包括 Kotlin、Android Gradle Plugin、AndroidX WorkManager、ZXing Android Embedded；测试使用 JUnit 和 Robolectric。确切版本由 Gradle 配置声明；各组件保留原许可证与 NOTICE。
- Cloudflare 使用 `hpack.js` 2.1.6（MIT）解析 HTTP/2 HPACK；完整依赖树由 package-lock.json 固定。Wrangler、TypeScript、Vitest 等为开发工具，依各自许可使用。
- iOS 使用 Apple 系统框架；系统 SDK 与 Xcode 不随仓库分发，受 Apple 相应条款约束。

发布二进制时应随所分发组件保留适用的许可证/NOTICE；本文件是索引，不替代第三方完整许可证。
