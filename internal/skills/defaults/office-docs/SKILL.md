---
name: office-docs
description: Read, analyze, create, and edit PDF, Excel (.xlsx/.xls/.ods/.csv/.tsv), Word (.docx), and PowerPoint (.pptx) documents in Hoxi Work & Code workspaces using built-in zero-dependency `hoxi doc` commands and structured templates. 在 Hoxi 工作区中读取、解析、生成与修改 PDF、Excel 表格（.xlsx/.xls/.ods/.csv/.tsv）、Word 文档（.docx）与 PPT 演示文稿，内置免 Python 依赖的 `hoxi doc` 原生命令与报表规范。
compatibility: Built-in Hoxi CLI (`hoxi doc`) + macOS/Windows native document extraction
metadata:
  author: "Hoxi"
  version: "1.0.0"
---

# Office & PDF 文档工作流技能 (`office-docs`)

`office-docs` 为 Hoxi Work 与 Hoxi Code 提供开箱即用、零外部运行时依赖的 **PDF、Excel (`.xlsx` / `.xls` / `.ods` / `.csv` / `.tsv`)、Word (`.docx`)、PPT (`.pptx`)** 读取、拆解、生成与单元格级修改能力。

---

## 一、文件接入与读取优先级

1. **会话附件直读**：
   - 用户在对话框上传、拖拽或粘贴的文档会自动落盘至 `<worktree>/.hoxi/attachments/<id>_<filename>`（该目录享有读写豁免），并在消息的 `<attached_file path="...">` 标签中附带提取摘要。
   - 若附件行数较多被截断（显示 `...[rows truncated]...` 或 `...[truncated]...`），直接使用内置 `hoxi doc` 命令按 Sheet 或按全量字符读取，**无需编写临时 Python 脚本**。
2. **优先使用内置 `hoxi doc` 命令（零依赖、跨平台）**：
   - Hoxi 二进制已内置 `hoxi doc` 子命令（自动注入会话 `PATH`），支持原生解析与生成 `.xlsx`、旧版 `.xls`（含 OLE2/BIFF8、XML 2003、ERP 导出的 HTML 表格 `.xls`）、`.ods`、`.csv`、`.tsv`、`.docx`、`.pptx`、`.pdf`，即使本机未安装 Python 也可直接完成读写闭环。

---

## 二、`hoxi doc` 原生命令速查

### 1. 检视与读取（Inspect & Read）

```bash
# 查看表格的所有 Sheet 名称、行列数与表头结构（输出 JSON）
hoxi doc inspect "<file.xlsx|file.xls|file.pdf|file.docx>"

# 读取指定 Sheet 并输出为 Markdown 表格（或 text / csv / tsv / json）
hoxi doc read "<file.xlsx>" --sheet "财务明细" --format md --max-rows 1000

# 提取 PDF / Word / PPT 全文
hoxi doc read "<file.pdf>" --max-chars 120000
```

### 2. 生成与修改 Excel 表格（`.xlsx` / `.csv`）

```bash
# 从内联 JSON 直接生成标准 .xlsx（自动识别数字、布尔值与 =SUM(...) 公式）
hoxi doc write-xlsx "output/q4-budget.xlsx" --sheet "预算汇总" --rows-json '[["科目","10月","11月","合计"],["服务器","1200","1350","=B2+C2"],["买量投放","8500","9200","=B3+C3"],["总计","=SUM(B2:B3)","=SUM(C2:C3)","=SUM(D2:D3)"]]'

# 从多 Sheet JSON 文件生成 .xlsx（格式：[{"name":"Sheet1","rows":[...]}, {"name":"Sheet2","rows":[...]}]）
hoxi doc write-xlsx "output/report.xlsx" --from-json "data/sheets.json"

# 从现有 CSV / TSV 转换为标准 .xlsx
hoxi doc write-xlsx "output/orders.xlsx" --sheet "订单明细" --from-csv "raw/orders.csv"

# 原地修改现有 .xlsx / .xls 的指定单元格或追加行（支持跨格式读取旧版 .xls 并输出为 .xlsx）
hoxi doc set-cell "output/q4-budget.xlsx" --sheet "预算汇总" B2=1500 C2=1600 --append-json '[["软件订阅","300","300","=B4+C4"]]'
```

### 3. 生成 Word 文档（`.docx`）与 PDF 文档（`.pdf`）

```bash
# 从 Markdown 文件或文本生成带标题层级（# / ## / ###）、列表与边框表格的标准 .docx
hoxi doc write-docx "output/weekly-report.docx" --title "Q4 运营复盘报告" --from "notes/summary.md"

# 直接从文本生成标准 .pdf（macOS 自动启用系统原生中英混排矢量渲染）
hoxi doc write-pdf "output/brief.pdf" --title "Executive Summary" --from "notes/summary.md"
```

---

## 三、表格与商务文档质量红线（必守）

1. **汇总与派生列优先写公式**：
   - 在生成或修改 `.xlsx` 财务/运营报表时，小计、合计、毛利率、环比等派生单元格应写入 Excel 公式（如 `=SUM(B2:B20)`、`=(B2-C2)/B2`），切勿只写死静态计算结果。
2. **保留前导零与长编号精度**：
   - 订单号、SKU 编码、银行卡号、邮政编码（如 `00128` 或超过 15 位的数字串）必须保持文本格式，防止被 Excel 截断精度或丢失前导零（`hoxi doc write-xlsx` 已内置该保护）。
3. **写后必验闭环**：
   - 每次生成或修改 `.xlsx` / `.docx` / `.pdf` 后，必须立即执行 `hoxi doc inspect "<output-file>"` 或 `hoxi doc read "<output-file>" --max-rows 20` 核验表头、行数与关键数值无误后再向用户交付文件链接。
