package templates

import (
	"strings"

	"knowforge/server/internal/plugincore"
)

const cfgSeeded = "templates_seeded"

type seedTemplate struct {
	Kind        string
	Title       string
	Description string
	Content     string
	Chapters    []Node
}

// seedOfficialTemplates 插件首次启用时预置几份常用的站点模板（按站点默认内容语言选择中文或英文）；
// 之后再启用不会重复预置，管理员删掉的也不会被加回来。
func seedOfficialTemplates(core plugincore.Core) error {
	if core.GetSetting(cfgSeeded) == "true" {
		return nil
	}
	db := core.Gorm()
	var n int64
	db.Model(&Template{}).Where("official = ?", true).Count(&n)
	if n == 0 {
		seeds := seedsZH
		if locale, err := core.DefaultContentLocale(); err == nil && strings.HasPrefix(strings.ToLower(locale), "en") {
			seeds = seedsEN
		}
		for i, s := range seeds {
			t := Template{Official: true, Kind: s.Kind, Title: s.Title, Description: s.Description, Content: s.Content, SortOrder: i}
			if s.Kind == KindBook {
				nodes, count, err := normalizeTree(s.Chapters)
				if err != nil {
					return err
				}
				t.Chapters, t.ChapterCount = encodeTree(nodes), count
			}
			if err := db.Create(&t).Error; err != nil {
				return err
			}
		}
	}
	return core.SetSetting(cfgSeeded, "true", "模板：已预置站点模板")
}

var seedsZH = []seedTemplate{
	{Kind: KindBook, Title: "产品文档", Description: "适合软件产品的使用文档：介绍、快速开始、安装部署、使用指南、常见问题与更新日志。", Chapters: []Node{
		{Title: "产品介绍", Content: "# {{book}}\n\n一句话介绍 {{book}} 是什么、解决什么问题。\n\n## 主要功能\n\n- 功能一\n- 功能二\n- 功能三\n\n## 适用场景\n\n说明谁会用到它、在什么情况下使用。\n"},
		{Title: "快速开始", Content: "# 快速开始\n\n用 5 分钟跑通第一个例子。\n\n## 准备工作\n\n- 运行环境要求\n- 需要的账号或密钥\n\n## 第一步\n\n```bash\n# 在这里写命令\n```\n\n## 下一步\n\n- 阅读[[安装部署]]了解完整的安装方式\n- 阅读[[使用指南]]了解更多功能\n"},
		{Title: "安装部署", Content: "# 安装部署\n\n## 系统要求\n\n| 项目 | 要求 |\n| --- | --- |\n| 操作系统 | |\n| 内存 | |\n\n## 安装步骤\n\n1. 第一步\n2. 第二步\n3. 第三步\n\n## 验证安装\n\n说明如何确认安装成功。\n"},
		{Title: "使用指南", Content: "# 使用指南\n\n按功能介绍日常使用方法，每个功能一个子章节。\n", Children: []Node{
			{Title: "基础功能", Content: "# 基础功能\n\n"},
			{Title: "高级功能", Content: "# 高级功能\n\n"},
		}},
		{Title: "常见问题", Content: "# 常见问题\n\n## 问题一？\n\n回答。\n\n## 问题二？\n\n回答。\n"},
		{Title: "更新日志", Content: "# 更新日志\n\n## {{date}}\n\n### 新增\n\n- \n\n### 修复\n\n- \n"},
	}},
	{Kind: KindBook, Title: "技术教程", Description: "按由浅入深组织的系列教程：前言、基础篇、进阶篇、实战项目与附录。", Chapters: []Node{
		{Title: "前言", Content: "# 前言\n\n## 这本教程适合谁\n\n## 你将学到什么\n\n## 如何阅读\n\n作者：{{author}}\n"},
		{Title: "基础篇", Content: "# 基础篇\n\n本篇介绍入门所需的基础知识。\n", Children: []Node{
			{Title: "环境准备", Content: "# 环境准备\n\n"},
			{Title: "核心概念", Content: "# 核心概念\n\n"},
		}},
		{Title: "进阶篇", Content: "# 进阶篇\n\n", Children: []Node{
			{Title: "原理解析", Content: "# 原理解析\n\n"},
			{Title: "最佳实践", Content: "# 最佳实践\n\n"},
		}},
		{Title: "实战项目", Content: "# 实战项目\n\n## 项目目标\n\n## 实现步骤\n\n## 小结\n"},
		{Title: "附录", Content: "# 附录\n\n## 参考资料\n\n## 术语表\n"},
	}},
	{Kind: KindBook, Title: "团队知识库", Description: "团队内部的知识沉淀：团队介绍、工作流程、技术规范、会议纪要与新人入门。", Chapters: []Node{
		{Title: "团队介绍", Content: "# 团队介绍\n\n## 职责\n\n## 成员与分工\n\n| 成员 | 负责 |\n| --- | --- |\n| | |\n"},
		{Title: "新人入门", Content: "# 新人入门\n\n## 第一周清单\n\n- [ ] 开通账号与权限\n- [ ] 阅读[[工作流程]]\n- [ ] 阅读[[技术规范]]\n"},
		{Title: "工作流程", Content: "# 工作流程\n\n"},
		{Title: "技术规范", Content: "# 技术规范\n\n"},
		{Title: "会议纪要", Content: "# 会议纪要\n\n每次会议在下面新建一个子章节，可使用「会议纪要」章节模板。\n"},
	}},
	{Kind: KindChapter, Title: "会议纪要", Description: "时间、参会人、议题、结论与待办事项。", Content: "# {{chapter}}\n\n- **时间**：{{datetime}}\n- **记录人**：{{author}}\n- **参会人**：\n\n## 议题\n\n1. \n\n## 讨论与结论\n\n\n## 待办事项\n\n- [ ] 事项（负责人，截止日期）\n"},
	{Kind: KindChapter, Title: "接口文档", Description: "一个 HTTP 接口的说明：地址、参数、返回值、示例与错误码。", Content: "# {{chapter}}\n\n一句话说明这个接口的用途。\n\n## 请求\n\n```http\nGET /api/v1/example\n```\n\n### 参数\n\n| 名称 | 位置 | 类型 | 必填 | 说明 |\n| --- | --- | --- | --- | --- |\n| id | path | integer | 是 | |\n\n## 返回\n\n```json\n{\n  \"success\": true,\n  \"data\": {}\n}\n```\n\n## 错误码\n\n| 状态码 | 说明 |\n| --- | --- |\n| 400 | 参数错误 |\n| 404 | 资源不存在 |\n"},
	{Kind: KindChapter, Title: "教程章节", Description: "学习目标、前置知识、分步讲解、小结与练习。", Content: "# {{chapter}}\n\n<Tip>\n**学习目标**\n\n读完本章，你将能够：\n\n- \n</Tip>\n\n## 前置知识\n\n\n## 步骤一\n\n\n## 步骤二\n\n\n## 小结\n\n\n## 练习\n\n1. \n"},
	{Kind: KindChapter, Title: "版本发布说明", Description: "一次版本发布的新增、改进、修复与升级提示。", Content: "# {{chapter}}\n\n发布日期：{{date}}\n\n## 新增\n\n- \n\n## 改进\n\n- \n\n## 修复\n\n- \n\n## 升级提示\n\n<Warning>\n升级前请先备份数据。\n</Warning>\n"},
	{Kind: KindChapter, Title: "常见问题", Description: "问答形式的常见问题。", Content: "# {{chapter}}\n\n## 问题一？\n\n回答。\n\n## 问题二？\n\n回答。\n\n## 没有找到答案？\n\n请联系 {{author}}。\n"},
}

var seedsEN = []seedTemplate{
	{Kind: KindBook, Title: "Product documentation", Description: "User docs for a software product: introduction, quick start, installation, user guide, FAQ and changelog.", Chapters: []Node{
		{Title: "Introduction", Content: "# {{book}}\n\nDescribe in one sentence what {{book}} is and which problem it solves.\n\n## Features\n\n- Feature one\n- Feature two\n- Feature three\n\n## Use cases\n\nWho uses it and when.\n"},
		{Title: "Quick start", Content: "# Quick start\n\nGet the first example running in five minutes.\n\n## Prerequisites\n\n- Requirements\n- Accounts or keys you need\n\n## Step one\n\n```bash\n# commands go here\n```\n\n## Next steps\n\n- Read [[Installation]] for the full setup\n- Read [[User guide]] to learn more\n"},
		{Title: "Installation", Content: "# Installation\n\n## Requirements\n\n| Item | Requirement |\n| --- | --- |\n| OS | |\n| Memory | |\n\n## Steps\n\n1. Step one\n2. Step two\n3. Step three\n\n## Verify\n\nHow to check that the installation works.\n"},
		{Title: "User guide", Content: "# User guide\n\nOne sub-chapter per feature.\n", Children: []Node{
			{Title: "Basics", Content: "# Basics\n\n"},
			{Title: "Advanced", Content: "# Advanced\n\n"},
		}},
		{Title: "FAQ", Content: "# FAQ\n\n## Question one?\n\nAnswer.\n\n## Question two?\n\nAnswer.\n"},
		{Title: "Changelog", Content: "# Changelog\n\n## {{date}}\n\n### Added\n\n- \n\n### Fixed\n\n- \n"},
	}},
	{Kind: KindBook, Title: "Technical tutorial", Description: "A step-by-step tutorial series: preface, basics, advanced topics, a project and appendix.", Chapters: []Node{
		{Title: "Preface", Content: "# Preface\n\n## Who this is for\n\n## What you will learn\n\n## How to read it\n\nAuthor: {{author}}\n"},
		{Title: "Basics", Content: "# Basics\n\n", Children: []Node{
			{Title: "Setting up", Content: "# Setting up\n\n"},
			{Title: "Core concepts", Content: "# Core concepts\n\n"},
		}},
		{Title: "Advanced topics", Content: "# Advanced topics\n\n", Children: []Node{
			{Title: "How it works", Content: "# How it works\n\n"},
			{Title: "Best practices", Content: "# Best practices\n\n"},
		}},
		{Title: "Project", Content: "# Project\n\n## Goal\n\n## Steps\n\n## Summary\n"},
		{Title: "Appendix", Content: "# Appendix\n\n## References\n\n## Glossary\n"},
	}},
	{Kind: KindBook, Title: "Team knowledge base", Description: "Internal team knowledge: about the team, onboarding, workflows, standards and meeting notes.", Chapters: []Node{
		{Title: "About the team", Content: "# About the team\n\n## Responsibilities\n\n## Members\n\n| Member | Owns |\n| --- | --- |\n| | |\n"},
		{Title: "Onboarding", Content: "# Onboarding\n\n## First week\n\n- [ ] Get accounts and access\n- [ ] Read [[Workflows]]\n- [ ] Read [[Standards]]\n"},
		{Title: "Workflows", Content: "# Workflows\n\n"},
		{Title: "Standards", Content: "# Standards\n\n"},
		{Title: "Meeting notes", Content: "# Meeting notes\n\nAdd one sub-chapter per meeting, using the \"Meeting notes\" chapter template.\n"},
	}},
	{Kind: KindChapter, Title: "Meeting notes", Description: "Time, attendees, agenda, decisions and action items.", Content: "# {{chapter}}\n\n- **Time**: {{datetime}}\n- **Notes by**: {{author}}\n- **Attendees**: \n\n## Agenda\n\n1. \n\n## Discussion and decisions\n\n\n## Action items\n\n- [ ] Item (owner, due date)\n"},
	{Kind: KindChapter, Title: "API reference", Description: "One HTTP endpoint: path, parameters, response, example and errors.", Content: "# {{chapter}}\n\nWhat this endpoint does.\n\n## Request\n\n```http\nGET /api/v1/example\n```\n\n### Parameters\n\n| Name | In | Type | Required | Description |\n| --- | --- | --- | --- | --- |\n| id | path | integer | yes | |\n\n## Response\n\n```json\n{\n  \"success\": true,\n  \"data\": {}\n}\n```\n\n## Errors\n\n| Status | Meaning |\n| --- | --- |\n| 400 | Bad request |\n| 404 | Not found |\n"},
	{Kind: KindChapter, Title: "Tutorial chapter", Description: "Goals, prerequisites, steps, summary and exercises.", Content: "# {{chapter}}\n\n<Tip>\n**Goals**\n\nAfter this chapter you will be able to:\n\n- \n</Tip>\n\n## Prerequisites\n\n\n## Step one\n\n\n## Step two\n\n\n## Summary\n\n\n## Exercises\n\n1. \n"},
	{Kind: KindChapter, Title: "Release notes", Description: "What is new, improved and fixed in a release, plus upgrade notes.", Content: "# {{chapter}}\n\nReleased: {{date}}\n\n## Added\n\n- \n\n## Improved\n\n- \n\n## Fixed\n\n- \n\n## Upgrade notes\n\n<Warning>\nBack up your data before upgrading.\n</Warning>\n"},
	{Kind: KindChapter, Title: "FAQ", Description: "Frequently asked questions.", Content: "# {{chapter}}\n\n## Question one?\n\nAnswer.\n\n## Question two?\n\nAnswer.\n\n## Still stuck?\n\nContact {{author}}.\n"},
}
