package controller

import (
	"log"
	"os"
	"os/exec"
	"time"
)

func (r *Reconciler) checkAndHeal(res Resource) error {
	exists, err := r.sm.DetectResourceExists(res.Kind, res.Name)
	if err != nil {
		return err
	}
	if exists {
		return nil // 正常
	}

	log.Printf("⚠️ 检测到资源缺失: %s/%s，启动自动自愈...", res.Kind, res.Name)

	for attempt := 1; attempt <= res.MaxRetry; attempt++ {
		log.Printf("🔧 自愈尝试 %d/%d: helm upgrade %s", attempt, res.MaxRetry, res.Name)

		cmd := exec.Command("helm", "upgrade", "--install", "--wait", "--force-conflicts",
			res.Name, "./deployments/web3-blitz", "--namespace", "web3-blitz",
			"--set", "image.tag="+os.Getenv("VERSION"))

		if output, err := cmd.CombinedOutput(); err == nil {
			log.Printf("✅ 自愈成功: %s", res.Name)
			return nil
		} else {
			log.Printf("[WARN] 自愈失败: %s", string(output))
			time.Sleep(3 * time.Second)
		}
	}

	// 自愈失败 → 自动 rollback
	if res.Fallback == "rollback" {
		log.Printf("🚨 自愈失败，执行 helm rollback %s", res.Name)
		cmd := exec.Command("helm", "rollback", res.Name, "--namespace", "web3-blitz", "--wait")
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("[ERROR] rollback 失败: %s", string(output))
		} else {
			log.Printf("✅ 已自动 rollback 到上一个版本")
		}
	}

	return nil
}
