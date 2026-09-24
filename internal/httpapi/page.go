package httpapi

import "net/http"

const indexHTML = `<!doctype html>
<html lang="zh">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>令牌流网 · 本地 Petri net 复核工作区</title>
<style>
:root{--bd:#cbd5e0;--mut:#2b6cb0;--bg:#f7fafc;}
*{box-sizing:border-box}
body{margin:0;font-family:-apple-system,"PingFang SC","Segoe UI",sans-serif;background:var(--bg);color:#1a202c}
header{background:#1a365d;color:#fff;padding:12px 20px;display:flex;gap:14px;align-items:center;flex-wrap:wrap}
header h1{font-size:16px;margin:0;font-weight:600}
button{border:1px solid var(--mut);background:#ebf4ff;color:#1a365d;border-radius:6px;padding:6px 12px;cursor:pointer;font-size:13px}
button:hover{background:#bee3f8}
button.danger{border-color:#c53030;color:#742a2a;background:#fff5f5}
button.ok{border-color:#2f855a;color:#22543d;background:#f0fff4}
select,input{padding:5px 8px;border:1px solid var(--bd);border-radius:6px;font-size:13px}
.wrap{display:grid;grid-template-columns:260px 1fr 360px;gap:14px;padding:14px}
.card{background:#fff;border:1px solid var(--bd);border-radius:10px;padding:12px;margin-bottom:12px}
.card h2{font-size:14px;margin:0 0 8px;color:#2c5282}
.card h3{font-size:13px;margin:10px 0 6px;color:#2d3748}
.small{font-size:12px;color:#4a5568}
.mono{font-family:ui-monospace,Menlo,monospace;font-size:11px}
.row{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin:6px 0}
.tag{display:inline-block;border-radius:10px;padding:1px 8px;font-size:11px;margin-right:4px}
.t-goal{background:#c6f6d5;color:#22543d}.t-dead{background:#fed7d7;color:#742a2a}
.t-front{background:#fef3c7;color:#744210}.t-omega{background:#e9d8fd;color:#44337a}
.t-stale{background:#edf2f7;color:#4a5568;text-decoration:line-through}
.branch{padding:8px;border:1px solid transparent;border-radius:8px;cursor:pointer;margin-bottom:6px;font-size:12px}
.branch.active{background:#ebf4ff;border-color:#90cdf4}
.cand{border:1px solid var(--bd);border-radius:8px;padding:8px;margin-bottom:8px;font-size:12px}
.cand.stale{opacity:.65;background:#edf2f7}
.dep{font-size:10px;color:#718096}
svg{max-width:100%;border:1px solid #e2e8f0;border-radius:8px;background:#fff}
pre{background:#1a202c;color:#e2e8f0;padding:10px;border-radius:8px;font-size:11px;overflow:auto;max-height:280px}
.tabs{display:flex;gap:6px;flex-wrap:wrap;margin-bottom:8px}
.tabs button.on{background:#2b6cb0;color:#fff}
.pill{font-size:10px;border:1px solid var(--bd);border-radius:10px;padding:0 7px;color:#4a5568}
table{border-collapse:collapse;font-size:11px;width:100%}
td,th{border:1px solid #e2e8f0;padding:3px 6px;text-align:left}
.banner{padding:8px 12px;border-radius:8px;font-size:12px;margin:8px 0;display:none}
.banner.show{display:block}
.banner.err{background:#fff5f5;border:1px solid #feb2b2;color:#742a2a}
.banner.info{background:#ebf8ff;border:1px solid #90cdf4;color:#2a4365}
.scroll{max-height:300px;overflow:auto}
</style></head>
<body>
<header>
  <h1>令牌流网 · Petri net 可达/覆盖/反例复核</h1>
  <button onclick="loadCase()">① 载入仓库案例 v1</button>
  <button onclick="buildCex()">② 生成反例档案(7类)</button>
  <button class="ok" onclick="conflictDemo()">③ 模拟双窗口冲突</button>
  <button onclick="exportArc()">导出归档</button>
  <label class="small" style="color:#bee3f8">导入归档: <input type="file" id="arcfile" accept="application/json"/></label>
  <span id="ver" class="small" style="color:#bee3f8"></span>
</header>
<div class="wrap">
  <div>
    <div class="card">
      <h2>分支（版本谱系）</h2>
      <div id="branches" class="scroll"></div>
      <h3>在当前分支上修订</h3>
      <div class="row"><button onclick="reviseParams()">参数修订 预算→90</button></div>
      <div class="row"><button onclick="reviseSourceV3()">局部源修订 v3(仅concurrency)</button></div>
      <div class="row"><button onclick="undoFirst()">撤销最近裁定</button></div>
    </div>
    <div class="card">
      <h2>历史事件（可回到旧版本）</h2>
      <div id="timeline" class="scroll mono"></div>
    </div>
  </div>
  <div>
    <div id="banner" class="banner"></div>
    <div class="card">
      <h2>复核视图 <span id="brtitle" class="pill"></span></h2>
      <div class="row">
        <label>情形 <select id="scenario" onchange="renderAll()"></select></label>
        <div class="tabs">
          <button data-tab="net" class="on" onclick="setTab('net')">网络图</button>
          <button data-tab="space" onclick="setTab('space')">状态空间/marking 路径</button>
          <button data-tab="cover" onclick="setTab('cover')">覆盖关系 ω</button>
          <button data-tab="deadlock" onclick="setTab('deadlock')">死锁原因</button>
          <button data-tab="frontier" onclick="setTab('frontier')">截断 frontier</button>
          <button data-tab="raw" onclick="setTab('raw')">原始来源(只读)</button>
        </div>
      </div>
      <div id="tab-net"><img id="svg-net" alt="net svg"/></div>
      <div id="tab-space" style="display:none"><div class="small" id="pathinfo"></div><img id="svg-space"/></div>
      <div id="tab-cover" style="display:none"><div id="coverpairs"></div><img id="svg-cover"/></div>
      <div id="tab-deadlock" style="display:none"><div id="deadlock"></div></div>
      <div id="tab-frontier" style="display:none"><div id="frontier"></div></div>
      <div id="tab-raw" style="display:none"><pre id="raw"></pre></div>
    </div>
    <div class="card">
      <h2>规则判定事件（机器可读）</h2>
      <pre id="engineevents" class="mono" style="max-height:220px"></pre>
    </div>
  </div>
  <div>
    <div class="card">
      <h2>候选与裁定（接受/否定/参数调整）</h2>
      <div class="small">分支版本（乐观锁）：<b id="brver"></b>
        <button onclick="refreshBranch()">刷新版本</button></div>
      <div id="candidates" class="scroll" style="max-height:360px"></div>
    </div>
    <div class="card">
      <h2>失效范围（依赖驱动）</h2>
      <div id="invs" class="scroll small" style="max-height:180px"></div>
    </div>
    <div class="card">
      <h2>诊断：无法转换保留原值</h2>
      <div id="diags" class="small"></div>
    </div>
  </div>
</div>
<script src="/static/app.js"></script>
</body></html>`

func (s *Server) indexPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}
