import java.util.Properties

plugins { id("com.android.application"); id("org.jetbrains.kotlin.android") }
android {
    namespace = "com.kuaapi.sms.forwarder"
    compileSdk = 36
    defaultConfig {
        applicationId = "com.kuaapi.sms.forwarder"
        minSdk = 26
        targetSdk = 36
        versionCode = 7
        versionName = "1.2.0"
        val endpoint = providers.gradleProperty("smsDefaultEndpoint").getOrElse("https://sms.example.com")
        require(endpoint.matches(Regex("https://[A-Za-z0-9.:-]+"))) { "smsDefaultEndpoint must be an HTTPS origin" }
        buildConfigField("String", "DEFAULT_ENDPOINT", "\"$endpoint\"")
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }
    compileOptions { sourceCompatibility = JavaVersion.VERSION_17; targetCompatibility = JavaVersion.VERSION_17 }
    kotlinOptions { jvmTarget = "17" }
    val localSigning = rootProject.file(providers.gradleProperty("androidSigningProperties")
        .getOrElse("../secrets/android-signing.properties"))
    if (localSigning.exists()) {
        val props = Properties().apply { localSigning.inputStream().use { load(it) } }
        signingConfigs.create("localRelease") {
            storeFile = file(props.getProperty("storeFile"))
            storePassword = props.getProperty("storePassword")
            keyAlias = props.getProperty("keyAlias")
            keyPassword = props.getProperty("keyPassword")
        }
    }
    buildTypes { release {
        isMinifyEnabled = false
        if (localSigning.exists()) signingConfig = signingConfigs.getByName("localRelease")
    } }
    buildFeatures { buildConfig = true }
    testOptions { unitTests.isIncludeAndroidResources = true }
    lint { abortOnError = true }
}
dependencies {
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
    implementation("androidx.work:work-runtime-ktx:2.10.1")
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.robolectric:robolectric:4.14.1")
    androidTestImplementation("androidx.test:runner:1.6.2")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")
}
