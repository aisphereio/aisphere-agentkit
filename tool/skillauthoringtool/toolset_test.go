package skillauthoringtool

import (
	"strings"
	"testing"
)

func TestQualityGateRequiresSourceFreeRuntimeBody(t *testing.T) {
	validBody := strings.Repeat("可执行动作。", 120) + `

## 适用场景
任意权力不对等的对白场景。

## 不适用场景
平等闲聊、情感互诉和不需要关系压迫的场景。

## 核心原理
用对白长度、回应速度、解释义务和评价权形成可感知的不对称。

## 执行步骤
1. 标定谁拥有命令权。
2. 让上位者少说，让下位者补全并行动。
3. 用旁观者反应确认秩序。

## 示例模板
上位者只给关键词；下位者完整回应并马上行动；旁观者停止插话。

## 失败模式
上位者解释过多、下位者没有行动压力、旁观者没有反应。

## 反过拟合规则
不绑定时代、角色、地点、组织和具体桥段，只保留结构动作。

## 验收标准
读者不看身份介绍也能判断谁拥有场面控制权。
`

	if qg := qualityGate("novel-dialogue-power-gap", "Use asymmetric dialogue to show power distance.", validBody); !qg.OK {
		t.Fatalf("expected source-free body to pass, got errors: %v", qg.Errors)
	}

	leakyBody := validBody + "\n## 来源证据\n来源书籍：《某书》；证据章节：第2章；source_artifacts: chapter_skill_pack_x.json"
	if qg := qualityGate("novel-dialogue-power-gap", "Use asymmetric dialogue to show power distance.", leakyBody); qg.OK {
		t.Fatalf("expected leaky body to fail")
	}
}
