plugins { kotlin("jvm") version "2.1.0"; application }
application { mainClass.set("com.example.ApplicationKt") }
repositories { mavenCentral() }
dependencies {
    implementation("io.ktor:ktor-server-netty:3.1.0")
    implementation("io.ktor:ktor-server-core:3.1.0")
}
tasks.register<Jar>("buildFatJar") {
    duplicatesStrategy = DuplicatesStrategy.EXCLUDE
    from(sourceSets.main.get().output)
    dependsOn(configurations.runtimeClasspath)
    from({ configurations.runtimeClasspath.get().map { if (it.isDirectory) it else zipTree(it) } })
    archiveClassifier.set("all")
}
