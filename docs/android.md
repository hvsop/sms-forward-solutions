# 安卓编译、安装与后台配置

需要 JDK 17、Android SDK Platform 36 / Build Tools 36.0.0。Android Studio 可以安装 SDK，工程自带 Gradle 8.13 Wrapper。最低 Android 8.0，当前 versionName=1.2.0、versionCode=7。

## 开发构建

```sh
cd android
# 将下面路径替换为本机 SDK 路径；local.properties 不提交 Git
printf 'sdk.dir=/path/to/Android/sdk\n' > local.properties
./gradlew :app:assembleDebug :app:testDebugUnitTest :app:lintDebug
```

安装 `app/build/outputs/apk/debug/app-debug.apk`。也可以 `adb install -r ...`。Debug 签名与正式签名不同，不能直接覆盖已安装的正式版；使用测试设备或不同 applicationId，避免卸载时丢失尚未上传的短信。

源码 namespace/package 保留 `com.kuaapi.sms.forwarder`；fork 若独立分发，可修改 `app/build.gradle.kts` 的 applicationId，以免与维护者安装包冲突。不要混用签名证书。发布版本递增 versionCode。

默认地址是示例 `https://sms.example.com`，用户可以在配对页修改；也可修改 `ServerEndpoint.DEFAULT` 和界面提示，不能将别人的私有服务作为默认公共服务。

## 正式签名

在仓库根目录创建自己的签名密钥（命令交互输入密码，不把密码写进 shell 历史）：

```sh
mkdir -p secrets
chmod 700 secrets
keytool -genkeypair -v -keystore secrets/android-release.jks \
  -alias release -keyalg RSA -keysize 3072 -validity 10000
```

创建 `secrets/android-signing.properties`，设置权限 0600：

```properties
storeFile=/absolute/private/path/android-release.jks
storePassword=YOUR_PRIVATE_PASSWORD
keyAlias=release
keyPassword=YOUR_PRIVATE_PASSWORD
```

```sh
chmod 600 secrets/android-signing.properties secrets/android-release.jks
cd android
./gradlew :app:assembleRelease :app:testDebugUnitTest :app:lintDebug
```

生成 `app/build/outputs/apk/release/app-release.apk`。使用 SDK 的 `apksigner verify --verbose --print-certs` 校验。没有签名配置时只生成 unsigned APK，不能将其当作已签名发布包。长期妥善备份密钥及密码，丢失后不能以原签名覆盖升级。

## 发布与安装

APK 可放 GitHub Releases，或后端固定 `/download/android.apk`。不要把 `.apk`、`.jks`、密码、账户邀请放入源码 Git 历史；Release 只上传 APK 和校验值，绝不上传签名密钥。

三星浏览器下载安装时可能要求允许此来源安装。系统限制短信权限时，检查应用详情的受限设置与短信权限；具体菜单因 Android/One UI 和安装来源不同而异。开源和签名不能保证消除系统风险提示；不要让用户全局关闭设备安全保护。

安装后按 [配对步骤](usage.md) 连接。允许 RECEIVE_SMS、后台运行和后台数据；三星将应用从休眠/深度休眠列表中移除，电池设置为不受限制。权限被撤回、强制停止、断电或系统限制时可能无法及时处理 SMS，应在实际机型验证。

转发范围：开启后的所有 SIM 新 SMS。关闭时新短信不入队、已有队列暂停；重新开启不会补回关闭期间的短信。队列最多 1,000 条，超过 7 天的条目在后续处理时清理。App 内“发送测试消息”不产生运营商短信费用，也不计入真实短信上传数。

## Google Play

AAB 构建、原签名托管、短信权限例外审核与测试要求见 [Google Play 发布](google-play.md)。安卓专用[隐私政策](android-privacy.md)说明后台接收和上传、相机、保留及删除。
