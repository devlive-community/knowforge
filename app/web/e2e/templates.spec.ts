import { expect, test } from '@playwright/test'
import { registerUser, signIn, unique } from './helpers'

// 模板：新建书籍时选择书籍模板，创建后按模板生成章节并打开第一章；写作台插入章节模板（变量替换），并把当前章节存为模板。

test('用模板新建书籍并插入章节模板', async ({ page }) => {
  const author = await registerUser('tpl')
  await signIn(page, author)

  // 新建书籍：选择站点书籍模板「产品文档」
  await page.goto('/books/create')
  await page.getByTestId('book-template-open').click()
  await page.getByTestId('template-option').filter({ hasText: '产品文档' }).click()
  await expect(page.getByTestId('template-picker')).toContainText('使用指南')
  await page.getByTestId('template-use').click()
  await expect(page.getByTestId('book-template-choice')).toContainText('将生成 8 个章节')
  const title = unique('模板书 ')
  await page.getByPlaceholder('给你的书起个名字').fill(title)
  await page.getByRole('button', { name: '创建书籍' }).click()

  // 进入写作台第一章，目录为模板章节，正文中的 {{book}} 已替换为书名
  await expect(page).toHaveURL(/\/book\/writer\/[^/]+\/[^/]+/)
  const editor = page.locator('textarea').first()
  await expect(editor).toHaveValue(new RegExp(`^# ${title}`))
  await expect(page.getByText('快速开始').first()).toBeVisible()

  // 插入章节模板「会议纪要」：{{chapter}} 替换为当前章节名
  await page.getByRole('button', { name: '模板', exact: true }).click()
  await page.getByTestId('template-option').filter({ hasText: '会议纪要' }).click()
  await expect(page.getByTestId('template-preview')).toContainText('待办事项')
  await page.getByTestId('template-use').click()
  await expect(editor).toHaveValue(/# 产品介绍\n\n- \*\*时间\*\*：\d{4}-\d{2}-\d{2} \d{2}:\d{2}/)

  // 把当前章节存为个人模板，在「我的模板」中可见
  await page.getByRole('button', { name: '模板', exact: true }).click()
  await page.getByTestId('template-save-chapter').click()
  await page.getByRole('dialog').getByRole('textbox').last().fill('我的章节模板')
  await page.getByRole('button', { name: '保存', exact: true }).last().click()
  await expect(page.getByTestId('template-option').filter({ hasText: '我的章节模板' })).toBeVisible()
  await page.keyboard.press('Escape')
  await page.goto('/user/templates')
  await expect(page.getByTestId('template-row').filter({ hasText: '我的章节模板' })).toBeVisible()
})
