package achievements

import (
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 仅供外部测试包（achievements_test）使用的内部入口。

type Settings = achievementSettings

const EvaluateJobType = achievementEvaluateJobType

func LoadSettings(core plugincore.Core) Settings {
	return (&behavior{core: core}).achievementSettings()
}

func EvaluateForUser(core plugincore.Core, userID uint, definition models.AchievementDefinition) error {
	return (&behavior{core: core}).evaluateAchievementForUser(userID, definition)
}

func EvaluateMetric(core plugincore.Core, userID uint, rule models.AchievementRule) (int64, error) {
	return (&behavior{core: core}).evaluateAchievementMetric(userID, rule)
}

// PresetCount 不依赖其他插件指标的预设数（其他插件未启用时首次启用会安装的数量）。
func PresetCount() int {
	n := 0
	for _, p := range presetAchievements {
		if _, ok := plugincore.UserMetricByKey(p.Metric); !ok {
			n++
		}
	}
	return n
}

// AllPresetCount 全部预设数。
func AllPresetCount() int { return len(presetAchievements) }
