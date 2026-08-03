---
name: novel-chapter-function-analysis
description: 对单个网文章节做功能分析，拆出钩子、冲突、爽点兑现、铺垫、节奏和追读价值。
allowed-tools:
  - BookPreprocessorToolset
  - list_artifacts
  - load_artifacts
  - save_artifact
metadata:
  display_name: 单章功能分析
  language: zh-CN
  output_language: zh-CN
  category: book_dissector
---
# 小说单章功能分析 Skill

## 目标

判断一个章节在连续网文中的具体功能，重点回答：为什么这章必须存在？它把读者从什么状态推到了什么状态？它为下一章制造了什么继续阅读的理由？

## 章节功能类型

常见功能包括但不限于：

1. 建立主角困境
2. 建立目标
3. 建立敌人或压力源
4. 制造误会或信息差
5. 铺垫能力、资源、道具、人脉
6. 兑现前文承诺
7. 制造小爽点
8. 制造大爽点前的压制
9. 转场进入新地图
10. 推动人物关系变化
11. 埋伏笔
12. 强化伏笔
13. 回收伏笔
14. 扩大世界观
15. 提升 stakes
16. 章尾悬念留钩

## 分析格式建议

分析一章时，优先按下面结构输出：

```json
{
  "chapter_no": 1,
  "summary": "",
  "main_function": "",
  "secondary_functions": [],
  "opening_hook": {
    "type": "",
    "reader_question": "",
    "technique": ""
  },
  "ending_hook": {
    "type": "",
    "unresolved_question": "",
    "next_chapter_pull": ""
  },
  "conflict": {
    "surface_conflict": "",
    "deep_conflict": "",
    "pressure_source": ""
  },
  "payoff": {
    "setup": "",
    "release": "",
    "emotion": ""
  },
  "foreshadowing_added": [],
  "foreshadowing_paid_off": [],
  "learnable_techniques": []
}
```

## 判断标准

好的章节功能分析必须具体到动作。例如：

- 不说“这一章推动剧情”，而说“这一章把主角从被动挨打推到主动反击，并用章尾未解决的追杀制造下一章期待”。
- 不说“有爽点”，而说“先让反派公开羞辱主角，再用隐藏能力反转局面，形成压制到释放的爽点”。
- 不说“埋伏笔”，而说“通过某个异常细节让读者记住一个问题，但暂不解释，为后续回收留空间”。
