# 第三方软件声明

LitePan 的前端按需使用以下第三方组件。各组件仍由其原作者持有版权，并遵循各自许可证。

## heic-to / libheif

- 用途：在浏览器不原生支持时解码 HEIC / HEIF 图片。
- 项目：https://github.com/hoppergee/heic-to
- 许可证：GNU Lesser General Public License v3.0 or later（LGPL-3.0-or-later）
- 许可证副本：前端静态资源 `/licenses/heic-to-LGPL-3.0.txt`

该组件作为独立的按需加载模块分发，LitePan 未修改其源码。

## libbitsub

- 用途：在浏览器内解析并渲染外置 Blu-ray PGS / SUP 位图字幕。
- 项目：https://github.com/altqx/libbitsub
- 许可证：MIT License
- 许可证副本：前端静态资源 `/licenses/libbitsub-MIT.txt`

该组件使用 WebAssembly，并且仅在用户实际选择 SUP 字幕时按需加载；LitePan 未修改其源码。

## docx-preview / docxjs

- 用途：在浏览器内解析并分页渲染 DOCX 文档。
- 项目：https://github.com/VolodymyrBaydalka/docxjs
- 许可证：Apache License 2.0

该组件仅在用户打开 DOCX 文档时按需加载；LitePan 未修改其源码。

## SheetJS Community Edition

- 用途：在浏览器内解析 XLSX、XLS、CSV 和 ODS 表格文件。
- 项目：https://git.sheetjs.com/SheetJS/sheetjs
- 许可证：Apache License 2.0

该组件仅在用户打开表格文件时按需加载；LitePan 未修改其源码。

## zip.js

- 用途：在浏览器内分段读取 ZIP / CBZ 压缩包的目录信息。
- 项目：https://github.com/gildas-lormeau/zip.js
- 许可证：BSD-3-Clause

该组件仅在用户打开 ZIP / CBZ 文件时按需加载；LitePan 未修改其源码。

## pptx-renderer

- 用途：在浏览器内解析并渲染 PPTX 演示文稿。
- 项目：https://github.com/aiden0z/pptx-renderer
- 许可证：Apache License 2.0

该组件仅在用户打开 PPTX 文件时按需加载，不向第三方服务上传文档；LitePan 未修改其源码。
