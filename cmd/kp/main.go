package main

import (
	"os"
	"strings"

	"github.com/Ixecd/kubepivot/internal/controller"
	"github.com/Ixecd/kubepivot/internal/logger"
)

func main() {
	logger.Init()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	if strings.HasPrefix(os.Args[1], "-") {
		runInit(os.Args[1:])
		return
	}

	switch os.Args[1] {
	case "init":
		runInit(os.Args[2:])
	case "sync":
		runSync(os.Args[2:])
	case "deploy":
		runDeploy(os.Args[2:])
	case "down":
		runDown(os.Args[2:])
	case "ai-plan":
		runAIPlan(os.Args[2:])
	case "doctor":
		runDoctor(os.Args[2:])
	case "upgrade":
		runUpgrade(os.Args[2:])
	case "history":
		runHistory(os.Args[2:])
	case "diff":
		runDiff(os.Args[2:])
	case "resume":
		runResume(os.Args[2:])
	case "status":
		runStatus(os.Args[2:])
	case "rollback":
		runRollback(os.Args[2:])
	case "release":
		runRelease(os.Args[2:])
	case "scan":
		runScan(os.Args[2:])
	case "warmup":
		runWarmup(os.Args[2:])
	case "promote":
		runPromote(os.Args[2:])
	case "migrate":
		runMigrate(os.Args[2:])
	case "compat":
		runCompat(os.Args[2:])
	case "pvc":
		runPVC(os.Args[2:])
	case "network":
		runNetwork(os.Args[2:])
	case "secret":
		runSecret(os.Args[2:])
	case "policy":
		runPolicy(os.Args[2:])
	case "audit":
		runAudit(os.Args[2:])
	case "supply-chain":
		runSupplyChain(os.Args[2:])
	case "sizing":
		runSizing(os.Args[2:])
	case "chaos":
		runChaos(os.Args[2:])
	case "plugin":
		runPlugin(os.Args[2:])
	case "version":
		runVersion()
	case "update":
		runSelfUpdate()
	case "context":
		runContext(os.Args[2:])
	case "sandbox":
		runSandbox(os.Args[2:])
	case "controller":
		if len(os.Args) > 2 && os.Args[2] == "start" {
			// pod 内部运行：启动 Reconciliation Loop
			controller.Start(os.Args[3:]...) // 跳过 "kp controller start"，剩下的传给 Start
		} else {
			// CLI 管理命令：install / uninstall / status / enroll / projects
			runController(os.Args[2:])
		}
	case "login":
		runLogin(os.Args[2:])
	case "team":
		runTeam(os.Args[2:])
	case "whoami":
		runWhoami(os.Args[2:])
	default:
		// 未知命令 → 尝试作为插件执行
		execPlugin(os.Args[1], os.Args[2:])
	}
}
