// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package sizing

// ─── MIG 分区模板 ────────────────────────────────────────────────

// MIGProfiles 定义常见 GPU 型号的合法 MIG 分区大小。
// Sizing 引擎推荐的 GPU_Mem 必须对齐到这些值之一。
// 数据来源：NVIDIA MIG 规范 (https://docs.nvidia.com/datacenter/tesla/mig-user-guide/)
var MIGProfiles = map[string][]int64{
	"A100-SXM4-40GB": {
		5 * 1024 * 1024 * 1024,  // 1g.5gb
		10 * 1024 * 1024 * 1024, // 2g.10gb
		20 * 1024 * 1024 * 1024, // 4g.20gb
		40 * 1024 * 1024 * 1024, // 8g.40gb (整卡)
	},
	"A100-SXM4-80GB": {
		10 * 1024 * 1024 * 1024,
		20 * 1024 * 1024 * 1024,
		40 * 1024 * 1024 * 1024,
		80 * 1024 * 1024 * 1024,
	},
	"H100-80GB": {
		10 * 1024 * 1024 * 1024,
		20 * 1024 * 1024 * 1024,
		40 * 1024 * 1024 * 1024,
		80 * 1024 * 1024 * 1024,
	},
}

// QuantizeGPUMem 将原始显存推荐值量化到最近的合法 MIG 分区大小。
// 如果 GPU 型号不支持 MIG 或无已知分区模板，返回原始值。
// 向上取最近的 profile：如推荐 12GB 且 A100 有 5/10/20/40，取 20GB。
//
// 注意：返回值单位是 Bytes，与 ResourceRequest.Memory 一致。
// ResourceRequest.GPU 是毫卡（整卡计数），两者不要混淆。
func QuantizeGPUMem(rawBytes int64, gpuProduct string) int64 {
	profiles, ok := MIGProfiles[gpuProduct]
	if !ok {
		return rawBytes // 未知型号，不量化
	}
	for _, p := range profiles {
		if rawBytes <= p {
			return p
		}
	}
	// 超出所有 profile → 整卡
	return profiles[len(profiles)-1]
}
