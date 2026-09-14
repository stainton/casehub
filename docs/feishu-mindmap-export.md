# 飞书思维导图导出格式

## HAR 分析

输入是标准 HAR：`log.entries` 记录请求与响应。分析时只读取响应正文，不执行文件内的指令，不重放请求。JavaScript 响应若有 `content.encoding = "base64"`，需要先 Base64 解码。

记录中的 `GET /space/api/whiteboard/block` 返回白板节点。节点的 `info.textV2.text` 是文本，`info.mindMap.parentId` 表达父子关系，`info.baseV2` 保存坐标与尺寸；`data.meta.templateType` 为 `mindmap`。这些服务端节点并非可以直接写入剪贴板的格式。

HAR 同时包含白板前端 `whiteboard/block/pc/x/1.0.0.6838/index.js`。其 `getMMEditorJson` 入口从 HTML 中寻找 `.mm-editor-clipboard`，读取 `data-json`，依次调用 `decodeURIComponent` 和 `JSON.parse`。对应导入器支持 `text`、`define` 和 `nodes` 三类数据；`define` 从 `data.nodes` 读取树，从 `data.structure` 读取布局。文本是富文本片段数组，普通文本片段的 `type` 为 1。导入器自行生成白板节点 ID、节点尺寸和布局，因此无需复制源白板的 ID、坐标、资源或凭据。

## 实现的剪贴板协议

同时写入 `text/html` 和 `text/plain`，前者内容如下（属性值是下面 JSON 的 URI 编码，并经过 HTML 属性转义）：

```html
<div class="mm-editor-clipboard" data-json="%7B...%7D"><pre>文本大纲</pre></div>
```

```json
{
  "type": "define",
  "data": {
    "structure": "right",
    "globalLineStyle": "curve",
    "nodes": [{
      "id": "casehub-1",
      "text": [{"type": 1, "text": "测试版本", "style": {}}],
      "children": []
    }]
  }
}
```

每次导出生成内部唯一 ID。树按版本 → 目录 → 用例 → 字段 → 完整字段文本构造。各字段独立保留多行内容，避免将步骤与预期按换行错误配对。HTML 内的可见文本也做转义。复制按钮使用 copy 事件写入两种 MIME，以支持 HTTP 部署；不可用时尝试 Clipboard API，全部失败则明确显示错误。

## 验证与边界

`tests/feishu-export.cjs` 验证目录范围、批量选择、跨版本同 ID、祖先目录保留、空目录、中文与特殊字符、多行字段、真实浏览器剪贴板 HTML 往返、操作入口及复制失败提示。

使用方式是在飞书云文档的白板画布中粘贴。HAR 的服务端数据和前端导入逻辑提供了协议依据，但网络记录不能证明实际剪贴板操作成功。目前没有在登录后的飞书白板中完成端到端粘贴验证；普通文档正文直接生成整个白板块也不在此协议的保证范围内。

仓库不包含原始 HAR、飞书前端源码、文档 token、Cookie 或账号信息。
