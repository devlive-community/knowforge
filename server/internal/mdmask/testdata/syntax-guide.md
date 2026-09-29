---
title: Markdown 语法与编辑器使用
description: KnowForge 写作台的使用方法，以及章节正文支持的全部 Markdown 语法：基础语法、章节元数据、章节链接、目录、提示块、标签页、折叠面板、步骤、卡片、网格、差异、公式、流程图、接口文档与行内扩展。
---

[toc]

## 写作台

章节在写作台中编辑，正文使用 Markdown。写作台分为三栏：左侧是书籍目录，中间是编辑区，右侧是章节设置（发布状态、父章节、排序、文档路径、外部链接、评论开关等）。

### 编辑与预览

顶栏右侧可以在三种模式间切换：

- **编辑**：只显示 Markdown 源码。
- **预览**：只显示渲染效果，与读者在阅读页看到的一致。
- **分栏**：左边写、右边实时预览，两侧滚动同步。

编辑区底部可以调整字号、进入专注模式（隐藏两侧栏，按 `Esc` 退出），并显示字数与预计阅读时长。修改会自动保存草稿，按 `Ctrl/⌘ + S` 立即保存；「发布」让读者可以看到本章。

### 快捷键

| 操作 | 快捷键 |
|---|---|
| 保存 | `Ctrl/⌘ + S` |
| 加粗 | `Ctrl/⌘ + B` |
| 斜体 | `Ctrl/⌘ + I` |
| 插入链接 | `Ctrl/⌘ + K` |
| 查找替换 | `Ctrl/⌘ + F` |
| 复制当前行 | `Ctrl/⌘ + Shift + D` |
| 上移 / 下移当前行 | `Alt + ↑` / `Alt + ↓` |
| 多行缩进 / 取消缩进 | 选中多行后按 `Tab` / `Shift + Tab` |
| 列表续行 | 在列表项末尾按 `Enter`，有序列表自动递增序号，任务列表新建未勾选项 |
| 退出列表 | 在空的列表项上按 `Enter` |

选中文字后直接输入 `(`、`[`、`{`、`` ` ``、`*`、`_`、`~`、`"`、`'`，会用这对符号把选中的文字包起来。选中文字后粘贴一个网址，会自动变成链接。

### 斜杠命令

在行首或空格后输入 `/`，弹出命令菜单，继续输入可以筛选（支持中文与英文关键词），用 `↑` `↓` 选择、`Enter` 或 `Tab` 确认、`Esc` 关闭。可用的命令有：一到四级标题、无序列表、有序列表、任务列表、引用、代码块、表格、标签页、备注、提示、警告、折叠面板、步骤、卡片组、子章节目录、分隔线、图片、链接，以及（启用相应功能时）采集网页、AI 写作助手、导入 Markdown 文件。

### 工具栏

工具栏提供加粗、斜体、删除线、链接、引用、行内代码、代码块、列表、任务列表、表格等常用格式，以及：

- **插入组件**：标签页、备注、提示、警告、折叠面板、步骤、卡片组、子章节目录。
- **章节元数据**：设置章节图标，见下文「章节元数据」。
- **上传图片 / 图片链接**：上传本地图片或插入网络图片。
- **采集网页内容**：输入网址，把网页正文转为 Markdown 插入到光标处。
- **导入 Markdown 文件**：把 `.md` 文件的内容导入到本章。
- **翻译**：翻译选中的文字或整章。
- **AI 写作助手**：续写、润色、改写等（站点启用时可用）。

### 图片

- 直接把图片**粘贴**或**拖入**编辑区，会自动上传并插入图片语法。
- 也可以用工具栏的「上传图片」选择文件。

### 链接到其他章节

在编辑区输入两个左方括号，会弹出本书的章节列表，输入文字筛选后按 `Enter` 插入双向链接，语法见下文「章节链接」。

## 章节元数据

章节可以在**文档开头**声明元数据，有两种写法，可以同时使用。只有写在文档最开头的才会被识别，正文中间出现的同样内容会按普通 Markdown 处理。

### front-matter

第一行必须是 `---`，到下一行 `---` 结束，中间每行一个 `键: 值`，值可以加引号：

```markdown
---
title: 常见用例指南
url: https://platform.claude.com/docs/zh-CN/about-claude/use-case-guides/overview
description: 探索用于构建常见 Claude 用例的生产指南：工单路由、客户支持智能体、内容审核、法律文书摘要和商务智能体。
icon: book
---

正文从这里开始。
```

| 键 | 作用 |
|---|---|
| `title` | 页面标题，用于浏览器标签页与分享；目录中仍显示章节名称 |
| `description` | 章节简介，显示在阅读页标题下方，也用于搜索引擎与分享摘要 |
| `url` | 原文地址（仅支持 `http://` 与 `https://`），阅读页标题下方显示「原文」链接 |
| `icon` | 章节图标，显示在书籍目录中 |

其他键会被保留但不使用。front-matter 中只要有一行不是 `键: 值` 的形式（缩进的续行除外），整块就不算元数据。

### 图标注释

```markdown
<!-- icon: database -->
```

写在文档开头（或紧跟在 front-matter 之后），只认第一条。图标使用 Font Awesome 图标名，如 `database`、`rocket`、`book`；品牌图标写成 `brands fa-github`。同时使用两种写法时，图标注释优先于 front-matter 中的 `icon`。

也可以用工具栏的「章节元数据 → 章节图标」设置：已有 front-matter 时写入其中，否则在开头插入图标注释；留空则移除图标。

元数据不会显示在正文中。写作台预览的顶部会显示当前的元数据，方便核对；导出 DOCX / EPUB 时不包含元数据，导出 Markdown 时原样保留。

## 基础语法

### 标题

```markdown
# 一级标题
## 二级标题
### 三级标题
#### 四级标题
##### 五级标题
###### 六级标题
```

Markdown 最多支持六级标题。章节名称已经作为页面的一级标题显示，正文建议从二级标题开始。二、三级标题会出现在阅读页右侧的「本章目录」中。

### 段落与换行

空一行开始新段落。段落内直接换行即可换行，不需要在行尾加空格。

### 强调

```markdown
**加粗**、*斜体*、~~删除线~~、`行内代码`
```

效果：**加粗**、*斜体*、~~删除线~~、`行内代码`

### 列表

```markdown
- 无序列表
- 第二项
  - 缩进两个空格为子项

1. 有序列表
2. 第二项

- [ ] 未完成的任务
- [x] 已完成的任务
```

效果：

- 无序列表
- 第二项
  - 缩进两个空格为子项

1. 有序列表
2. 第二项

- [ ] 未完成的任务
- [x] 已完成的任务

### 引用

```markdown
> 引用的文字，可以包含**格式**。
```

> 引用的文字，可以包含**格式**。

### 代码块

用三个反引号包围代码，并在开头注明语言以获得语法高亮；不写语言时会自动识别：

````markdown
```python
def hello(name):
    print(f"Hello, {name}")
```
````

效果：

```python
def hello(name):
    print(f"Hello, {name}")
```

### 链接

```markdown
[KnowForge](https://github.com/devlive-community/knowforge)
<https://example.com>
```

站外链接会在新窗口打开。

### 图片

```markdown
![图片说明](https://example.com/cover.png)
![图片说明](https://example.com/cover.png "标题")
```

可以在地址后面追加尺寸与对齐方式（宽 x 高，单位为像素，可只写其中一个；对齐为 `left`、`center`、`right`，左右对齐时文字环绕图片）：

```markdown
![示意图](https://example.com/cover.png =320x)
![示意图](https://example.com/cover.png =320x180 center)
![示意图](https://example.com/cover.png "标题" =x120 right)
```

### 表格

```markdown
| 左对齐 | 居中 | 右对齐 |
|:---|:---:|---:|
| 苹果 | 3 | ¥12.00 |
| **香蕉** | 12 | ¥8.50 |
```

效果：

| 左对齐 | 居中 | 右对齐 |
|:---|:---:|---:|
| 苹果 | 3 | ¥12.00 |
| **香蕉** | 12 | ¥8.50 |

表格的每一行都要以 `|` 开头和结尾；分隔行用 `:` 标记对齐方式，单元格内可以使用行内格式。

### 分隔线

```markdown
---
```

注意：写在文档第一行的 `---` 会被当作 front-matter 的开始（后面的内容符合 `键: 值` 格式时），分隔线不要放在文档开头。

### HTML

正文可以使用常见的 HTML 标签。出于安全考虑，脚本、事件属性等会被自动移除。

## 章节链接

### 双向链接

用两个方括号包住章节标题或文档路径，链接到本书的其他章节：

```markdown
[[安装指南]]
[[setup]]
[[安装指南|点这里查看安装步骤]]
[[other-book/getting-started]]
```

- 先按文档路径、再按章节标题（不区分大小写）查找本书章节。
- `|` 后面是显示的文字。
- 含 `/` 时表示其他书籍：`书籍路径/章节路径`。
- 找不到目标时显示为「缺失链接」样式，方便发现失效链接。

被链接的章节底部会显示「被引用」，列出引用了它的章节；书籍设置中可以查看全书的章节链接与失效链接。

### 文档链接

也可以用普通链接语法，以 `doc:` 开头：

```markdown
[安装指南](doc:setup)
[其他书籍的章节](doc:other-book/getting-started)
```

## 目录

### 本章目录

单独一行写 `[toc]`，会在该位置生成本章目录，包含一到六级的全部标题，按层级缩进，点击跳转到对应位置：

```markdown
[toc]
```

本页开头的目录就是这样生成的。

### 子章节目录

单独一行写 `[children]`，阅读页会在该位置列出本章的直接子章节；没有子章节时不显示。适合放在只做分组的父章节中：

```markdown
[children]
```

## 提示块

### GitHub 写法

```markdown
> [!NOTE]
> 备注：补充说明。

> [!TIP]
> 提示：更好的做法。

> [!IMPORTANT]
> 重要：必须知道的信息。

> [!WARNING]
> 警告：可能出现问题。

> [!CAUTION]
> 注意：操作有风险。
```

效果：

> [!NOTE]
> 备注：补充说明。

> [!TIP]
> 提示：更好的做法。

> [!IMPORTANT]
> 重要：必须知道的信息。

> [!WARNING]
> 警告：可能出现问题。

> [!CAUTION]
> 注意：操作有风险。

### 标签写法

也支持 `<Note>`、`<Tip>`、`<Info>`、`<Warning>`、`<Caution>`、`<Important>` 标签。开头的一行加粗文字会作为标题，省略时使用默认标题：

```markdown
<Tip>
**使用缓存**

重复的请求可以开启缓存，节省时间与费用。
</Tip>

<Warning>
删除后无法恢复。
</Warning>
```

效果：

<Tip>
**使用缓存**

重复的请求可以开启缓存，节省时间与费用。
</Tip>

<Warning>
删除后无法恢复。
</Warning>

提示块中可以使用任意 Markdown，包括代码块与其他组件。

## 标签页

两种写法效果相同。

### 标签写法

````markdown
<Tabs>
<Tab title="macOS">

```bash
brew install git-lfs
```

</Tab>
<Tab title="Ubuntu">

```bash
sudo apt install git-lfs
```

</Tab>
</Tabs>
````

效果：

<Tabs>
<Tab title="macOS">

```bash
brew install git-lfs
```

</Tab>
<Tab title="Ubuntu">

```bash
sudo apt install git-lfs
```

</Tab>
</Tabs>

### 冒号写法

```markdown
:::tabs
=== "Python"
使用 pip 安装。

=== "Node.js"
使用 npm 安装。
:::
```

效果：

:::tabs
=== "Python"
使用 pip 安装。

=== "Node.js"
使用 npm 安装。
:::

## 折叠面板

````markdown
<AccordionGroup>
<Accordion title="支持哪些数据库？">
支持 SQLite、MySQL 与 PostgreSQL。
</Accordion>
<Accordion title="如何升级？">
在后台「系统」页面一键升级。
</Accordion>
</AccordionGroup>
````

效果：

<AccordionGroup>
<Accordion title="支持哪些数据库？">
支持 SQLite、MySQL 与 PostgreSQL。
</Accordion>
<Accordion title="如何升级？">
在后台「系统」页面一键升级。
</Accordion>
</AccordionGroup>

`<Accordion>` 也可以单独使用，不放在 `<AccordionGroup>` 中。

## 步骤

```markdown
<Steps>
<Step title="安装">
下载并安装客户端。
</Step>
<Step title="登录">
使用账号登录。
</Step>
<Step title="开始写作">
新建一本书。
</Step>
</Steps>
```

效果：

<Steps>
<Step title="安装">
下载并安装客户端。
</Step>
<Step title="登录">
使用账号登录。
</Step>
<Step title="开始写作">
新建一本书。
</Step>
</Steps>

## 卡片

```markdown
<CardGroup cols={2}>
<Card title="工单路由" icon="headset" href="/books">
按规则把新工单分配给对应的**客服组**。
</Card>
<Card title="自动回复" icon="robot">
为常见问题配置回复模板。
</Card>
<Card title="服务协议" icon="file-contract" horizontal>
设置响应与解决时限。
</Card>
<Card title="GitHub 集成" icon="github" iconType="brands" color="#24292f" href="https://github.com">
从 Issue 同步工单。
</Card>
</CardGroup>
```

效果：

<CardGroup cols={2}>
<Card title="工单路由" icon="headset" href="/books">
按规则把新工单分配给对应的**客服组**。
</Card>
<Card title="自动回复" icon="robot">
为常见问题配置回复模板。
</Card>
<Card title="服务协议" icon="file-contract" horizontal>
设置响应与解决时限。
</Card>
<Card title="GitHub 集成" icon="github" iconType="brands" color="#24292f" href="https://github.com">
从 Issue 同步工单。
</Card>
</CardGroup>

| 属性 | 说明 |
|---|---|
| `title` | 卡片标题 |
| `icon` | Font Awesome 图标名（如 `headset`），也可以是图片地址 |
| `iconType` | 图标样式，默认实心；`regular` 为线框，`brands` 为品牌图标 |
| `color` | 图标颜色，如 `#24292f` |
| `href` | 点击卡片跳转的地址，站外链接在新窗口打开 |
| `horizontal` | 图标与标题排在同一行 |
| `cols` | 写在 `<CardGroup>` 上，列数 1–4，默认 2；窄屏自动变为一列 |

卡片正文支持 Markdown；`<Card>` 也可以单独使用。

## 网格

用列表项分格，每个列表项是一格，缩进的续行属于同一格：

```markdown
:::grid cols-3 gap-4
- **快速**
  毫秒级响应。
- **安全**
  全程加密。
- **开放**
  开源免费。
:::
```

效果：

:::grid cols-3 gap-4
- **快速**
  毫秒级响应。
- **安全**
  全程加密。
- **开放**
  开源免费。
:::

选项：`cols-1` 到 `cols-6` 设置列数（默认 2），`gap-0` 到 `gap-12` 设置间距（默认 4），加上 `no-responsive` 时窄屏也保持列数不变。

## 代码差异

行首的 `+`、`-` 标记新增与删除的行：

```markdown
:::diff
 function greet() {
-  console.log("Hello")
+  console.log("Hello, world")
 }
:::
```

效果：

:::diff
 function greet() {
-  console.log("Hello")
+  console.log("Hello, world")
 }
:::

也可以在开头按行号标记，不修改代码本身，如 `:::diff +2 -4,6-8` 表示第 2 行为新增，第 4 行和第 6 到 8 行为删除。

## 数学公式

```markdown
:::katex
E = mc^2 \qquad \int_0^1 x^2\,dx = \frac{1}{3}
:::
```

效果：

:::katex
E = mc^2 \qquad \int_0^1 x^2\,dx = \frac{1}{3}
:::

使用 KaTeX 语法，公式单独成块居中显示。

## 流程图

```markdown
:::mermaid
flowchart LR
  A[写作] --> B{审核}
  B -->|通过| C[发布]
  B -->|退回| A
:::
```

效果：

:::mermaid
flowchart LR
  A[写作] --> B{审核}
  B -->|通过| C[发布]
  B -->|退回| A
:::

使用 Mermaid 语法，支持流程图、时序图、类图、甘特图等。

## 接口文档

开头写请求方法与路径，其后是接口说明；`=== "小节名"` 开始一个小节，小节内容需要缩进四个空格：

````markdown
:::api GET /api/v1/books/{id}
获取书籍详情。

=== "请求参数"
    | 参数 | 类型 | 说明 |
    |---|---|---|
    | id | 整数 | 书籍 ID |

=== "响应示例"
    ```json
    { "id": 1, "title": "示例书籍" }
    ```
:::
````

效果：

:::api GET /api/v1/books/{id}
获取书籍详情。

=== "请求参数"
    | 参数 | 类型 | 说明 |
    |---|---|---|
    | id | 整数 | 书籍 ID |

=== "响应示例"
    ```json
    { "id": 1, "title": "示例书籍" }
    ```
:::

支持的方法：`GET`、`POST`、`PUT`、`PATCH`、`DELETE`。

## 行内扩展

### 按钮

```markdown
!btn[开始使用](/books)
!btn[下载](https://example.com/download){bg-emerald-600 text-white hover:bg-emerald-700}
```

效果：!btn[开始使用](/books)

方括号内是文字，圆括号内是链接（可省略），花括号内是自定义样式类（可省略）。

### 悬浮提示

```markdown
!tip[SSE](Server-Sent Events，服务端推送事件)
```

效果：鼠标移到 !tip[SSE](Server-Sent Events，服务端推送事件) 上查看说明。

### 开关

```markdown
!switch[自动保存](on)
!switch[夜间模式](off)
```

效果：!switch[自动保存](on) !switch[夜间模式](off)

状态写 `on`、`true`、`yes` 或 `1` 时为开启，只用于展示。

### 图标

```markdown
:rocket:
:heart{24,#e11d48}:
```

效果：:rocket: :heart{24,#e11d48}:

两个冒号之间写 Lucide 图标名，花括号内可选大小（像素）与颜色。

### GitHub Issue

```markdown
devlive-community/knowforge#1
```

效果：devlive-community/knowforge#1

`仓库所有者/仓库名` 后接井号与编号，显示为跳转到该 Issue 的徽章。只写井号加编号时默认指向 KnowForge 仓库，正文中需要写「井号 + 数字」的普通文字时，请放在行内代码中。
