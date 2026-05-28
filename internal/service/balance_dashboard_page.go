package service

const balanceDashboardHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>余额看板</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f4f7fb;
      --panel: #ffffff;
      --panel-soft: #f8fafc;
      --text: #172033;
      --muted: #667085;
      --line: #d9e2ec;
      --blue: #1967d2;
      --green: #0f9d58;
      --red: #d93025;
      --amber: #b06000;
      --shadow: 0 18px 48px rgba(18, 32, 54, .10);
    }

    * {
      box-sizing: border-box;
    }

    body {
      margin: 0;
      min-height: 100vh;
      background:
        linear-gradient(180deg, rgba(244, 247, 251, .95), rgba(239, 244, 249, 1)),
        #f4f7fb;
      color: var(--text);
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", "Microsoft YaHei", sans-serif;
      letter-spacing: 0;
    }

    main {
      width: min(1180px, calc(100vw - 32px));
      margin: 0 auto;
      padding: 28px 0 36px;
    }

    header {
      display: flex;
      align-items: flex-end;
      justify-content: space-between;
      gap: 18px;
      padding: 10px 0 22px;
    }

    h1 {
      margin: 0;
      font-size: clamp(26px, 4vw, 44px);
      line-height: 1.05;
      font-weight: 760;
    }

    .subhead {
      margin: 8px 0 0;
      color: var(--muted);
      font-size: 14px;
    }

    .status {
      min-width: 178px;
      display: inline-flex;
      align-items: center;
      justify-content: flex-end;
      gap: 9px;
      color: var(--muted);
      font-size: 13px;
      white-space: nowrap;
    }

    .dot {
      width: 10px;
      height: 10px;
      border-radius: 50%;
      background: var(--amber);
      box-shadow: 0 0 0 4px rgba(176, 96, 0, .13);
    }

    .dot.live {
      background: var(--green);
      box-shadow: 0 0 0 4px rgba(15, 157, 88, .14);
    }

    .dot.offline {
      background: var(--red);
      box-shadow: 0 0 0 4px rgba(217, 48, 37, .12);
    }

    .grid {
      display: grid;
      grid-template-columns: minmax(0, 1.55fr) minmax(320px, .8fr);
      gap: 18px;
      align-items: stretch;
    }

    .panel {
      background: rgba(255, 255, 255, .92);
      border: 1px solid rgba(217, 226, 236, .9);
      border-radius: 8px;
      box-shadow: var(--shadow);
      overflow: hidden;
    }

    .hero {
      display: grid;
      grid-template-columns: minmax(0, 1fr) auto;
      gap: 18px;
      align-items: end;
      padding: 22px;
    }

    .label {
      margin: 0 0 8px;
      color: var(--muted);
      font-size: 13px;
      font-weight: 650;
    }

    .balance {
      display: flex;
      align-items: baseline;
      gap: 12px;
      min-width: 0;
    }

    .balance strong {
      display: block;
      min-width: 0;
      overflow-wrap: anywhere;
      font-size: clamp(44px, 8vw, 82px);
      line-height: .95;
      font-weight: 800;
      font-variant-numeric: tabular-nums;
    }

    .balance span {
      color: var(--muted);
      font-size: 18px;
      font-weight: 700;
    }

    .delta {
      justify-self: end;
      min-width: 144px;
      padding: 12px 14px;
      border-radius: 8px;
      background: var(--panel-soft);
      border: 1px solid var(--line);
      text-align: right;
      font-variant-numeric: tabular-nums;
    }

    .delta .value {
      display: block;
      color: var(--red);
      font-size: 22px;
      font-weight: 760;
    }

    .delta.positive .value {
      color: var(--green);
    }

    .delta.neutral .value {
      color: var(--muted);
    }

    .metrics {
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      border-top: 1px solid var(--line);
      background: var(--panel-soft);
    }

    .metric {
      min-width: 0;
      padding: 16px 18px;
      border-right: 1px solid var(--line);
    }

    .metric:last-child {
      border-right: 0;
    }

    .metric b {
      display: block;
      min-height: 26px;
      overflow-wrap: anywhere;
      font-size: 22px;
      line-height: 1.15;
      font-variant-numeric: tabular-nums;
    }

    .metric span {
      display: block;
      margin-top: 4px;
      color: var(--muted);
      font-size: 12px;
      font-weight: 650;
    }

    .chart-panel {
      margin-top: 18px;
      padding: 18px 18px 12px;
    }

    .panel-title {
      display: flex;
      justify-content: space-between;
      gap: 12px;
      margin-bottom: 12px;
      color: var(--muted);
      font-size: 13px;
      font-weight: 700;
    }

    svg {
      display: block;
      width: 100%;
    }

    #lineChart {
      height: 318px;
    }

    #barChart {
      height: 154px;
    }

    .axis {
      color: #91a2b4;
      font-size: 11px;
    }

    .empty {
      display: grid;
      min-height: 230px;
      place-items: center;
      color: var(--muted);
      text-align: center;
      border: 1px dashed #c7d3df;
      border-radius: 8px;
      background: #fbfcfe;
    }

    .side {
      display: grid;
      gap: 18px;
    }

    .side .panel {
      box-shadow: 0 12px 34px rgba(18, 32, 54, .08);
    }

    .list {
      display: grid;
      gap: 1px;
      background: var(--line);
      border-top: 1px solid var(--line);
    }

    .row {
      display: grid;
      grid-template-columns: minmax(0, 1fr) auto;
      gap: 12px;
      align-items: center;
      min-height: 54px;
      padding: 12px 16px;
      background: #fff;
      font-size: 13px;
    }

    .row time {
      display: block;
      color: var(--muted);
      font-size: 12px;
    }

    .row strong {
      font-variant-numeric: tabular-nums;
    }

    .row .down {
      color: var(--red);
    }

    .row .up {
      color: var(--green);
    }

    .small-panel {
      padding: 16px;
    }

    .two-col {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 10px;
    }

    .mini {
      min-width: 0;
      padding: 12px;
      background: var(--panel-soft);
      border: 1px solid var(--line);
      border-radius: 8px;
    }

    .mini b {
      display: block;
      overflow-wrap: anywhere;
      font-size: 19px;
      line-height: 1.2;
      font-variant-numeric: tabular-nums;
    }

    .mini span {
      display: block;
      margin-top: 3px;
      color: var(--muted);
      font-size: 12px;
      font-weight: 650;
    }

    @media (max-width: 920px) {
      main {
        width: min(100vw - 22px, 760px);
        padding-top: 18px;
      }

      header,
      .hero {
        align-items: flex-start;
        grid-template-columns: 1fr;
      }

      header {
        flex-direction: column;
      }

      .status,
      .delta {
        justify-self: stretch;
        width: 100%;
        justify-content: flex-start;
        text-align: left;
      }

      .grid {
        grid-template-columns: 1fr;
      }

      .metrics {
        grid-template-columns: 1fr 1fr;
      }

      .metric:nth-child(2) {
        border-right: 0;
      }

      .metric:nth-child(1),
      .metric:nth-child(2) {
        border-bottom: 1px solid var(--line);
      }
    }

    @media (max-width: 520px) {
      .hero {
        padding: 18px;
      }

      .metrics,
      .two-col {
        grid-template-columns: 1fr;
      }

      .metric {
        border-right: 0;
        border-bottom: 1px solid var(--line);
      }

      .metric:last-child {
        border-bottom: 0;
      }

      .balance {
        align-items: flex-start;
        flex-direction: column;
        gap: 6px;
      }

      #lineChart {
        height: 260px;
      }
    }
  </style>
</head>
<body>
  <main>
    <header>
      <div>
        <h1>余额看板</h1>
        <p class="subhead" id="rangeText">读取日志中</p>
      </div>
      <div class="status" aria-live="polite"><span class="dot" id="dot"></span><span id="statusText">连接中</span></div>
    </header>

    <section class="grid">
      <div>
        <section class="panel">
          <div class="hero">
            <div>
              <p class="label">当前余额</p>
              <div class="balance"><strong id="currentBalance">--</strong><span>credits</span></div>
            </div>
            <div class="delta neutral" id="todayBox">
              <span class="label">今日变化</span>
              <span class="value" id="todayChange">--</span>
            </div>
          </div>
          <div class="metrics">
            <div class="metric"><b id="totalSpent">--</b><span>累计消耗</span></div>
            <div class="metric"><b id="spendPerHour">--</b><span>平均每小时</span></div>
            <div class="metric"><b id="minBalance">--</b><span>最低余额</span></div>
            <div class="metric"><b id="samples">--</b><span>有效样本</span></div>
          </div>
        </section>

        <section class="panel chart-panel">
          <div class="panel-title"><span>余额走势</span><span id="pointCount">--</span></div>
          <div id="lineEmpty" class="empty">暂无有效余额日志</div>
          <svg id="lineChart" role="img" aria-label="余额走势"></svg>
        </section>
      </div>

      <aside class="side">
        <section class="panel">
          <div class="small-panel">
            <div class="panel-title"><span>最近变化</span><span id="changeCount">--</span></div>
          </div>
          <div class="list" id="changes"></div>
        </section>

        <section class="panel small-panel">
          <div class="panel-title"><span>小时聚合</span><span id="bucketCount">--</span></div>
          <svg id="barChart" role="img" aria-label="小时消耗"></svg>
        </section>

        <section class="panel small-panel">
          <div class="two-col">
            <div class="mini"><b id="lastTime">--</b><span>最新记录</span></div>
            <div class="mini"><b id="ignored">--</b><span>已忽略记录</span></div>
            <div class="mini"><b id="avgDuration">--</b><span>平均耗时</span></div>
            <div class="mini"><b id="windowHours">--</b><span>统计跨度</span></div>
          </div>
        </section>
      </aside>
    </section>
  </main>

  <script>
    const state = {
      source: null,
      fallback: null,
      lastPayload: null
    };

    const els = {
      dot: document.getElementById("dot"),
      statusText: document.getElementById("statusText"),
      rangeText: document.getElementById("rangeText"),
      currentBalance: document.getElementById("currentBalance"),
      todayBox: document.getElementById("todayBox"),
      todayChange: document.getElementById("todayChange"),
      totalSpent: document.getElementById("totalSpent"),
      spendPerHour: document.getElementById("spendPerHour"),
      minBalance: document.getElementById("minBalance"),
      samples: document.getElementById("samples"),
      pointCount: document.getElementById("pointCount"),
      changeCount: document.getElementById("changeCount"),
      bucketCount: document.getElementById("bucketCount"),
      changes: document.getElementById("changes"),
      lineChart: document.getElementById("lineChart"),
      barChart: document.getElementById("barChart"),
      lineEmpty: document.getElementById("lineEmpty"),
      lastTime: document.getElementById("lastTime"),
      ignored: document.getElementById("ignored"),
      avgDuration: document.getElementById("avgDuration"),
      windowHours: document.getElementById("windowHours")
    };

    const money = new Intl.NumberFormat("zh-CN", { minimumFractionDigits: 2, maximumFractionDigits: 6 });
    const compact = new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 });
    const clock = new Intl.DateTimeFormat("zh-CN", { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
    const dayClock = new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });

	function connect() {
		if ("EventSource" in window) {
			const source = new EventSource("/api/balance/events");
			state.source = source;
			source.addEventListener("open", () => setConnection("live", "实时连接"));
			source.addEventListener("balance", event => render(JSON.parse(event.data)));
			source.addEventListener("error", () => {
				setConnection("offline", "重连中");
			});
			window.addEventListener("beforeunload", () => source.close());
			return;
		}
		startFallback();
	}

    function startFallback() {
      if (state.fallback) return;
      setConnection("offline", "轮询连接");
      loadOnce();
      state.fallback = window.setInterval(loadOnce, 1000);
    }

    async function loadOnce() {
      try {
        const response = await fetch("/api/balance", { cache: "no-store" });
        if (!response.ok) throw new Error(await response.text());
        render(await response.json());
        if (!state.source) setConnection("live", "轮询连接");
      } catch (error) {
        setConnection("offline", "连接异常");
      }
    }

    function setConnection(kind, text) {
      els.dot.className = "dot" + (kind === "live" ? " live" : kind === "offline" ? " offline" : "");
      els.statusText.textContent = text;
    }

    function render(payload) {
      state.lastPayload = payload;
      const summary = payload.summary || {};
      const current = payload.current;
      if (!current) {
        els.currentBalance.textContent = "--";
        els.rangeText.textContent = "暂无有效余额日志";
        els.lineEmpty.style.display = "grid";
        els.lineChart.style.display = "none";
        els.changes.innerHTML = '<div class="row"><div>暂无余额变化<time>等待日志写入</time></div><strong>--</strong></div>';
        return;
      }

      els.currentBalance.textContent = money.format(current.balance);
      els.todayChange.textContent = signed(summary.today_change);
      els.todayBox.className = "delta " + deltaClass(summary.today_change);
      els.totalSpent.textContent = money.format(summary.total_spent || 0);
      els.spendPerHour.textContent = money.format(summary.spend_per_hour || 0);
      els.minBalance.textContent = money.format(summary.min_balance || 0);
      els.samples.textContent = compact.format(summary.sample_count || 0);
      els.pointCount.textContent = (payload.series || []).length + " 点";
      els.changeCount.textContent = (payload.changes || []).length + " 次";
      els.bucketCount.textContent = (payload.hourly || []).length + " 小时";
      els.lastTime.textContent = dayClock.format(new Date(current.time));
      els.ignored.textContent = compact.format((payload.ignored && payload.ignored.total) || 0);
      els.avgDuration.textContent = compact.format(summary.average_duration_ms || 0) + " ms";
      els.windowHours.textContent = compact.format(summary.window_hours || 0) + " h";
      els.rangeText.textContent = formatRange(summary.first_time, summary.last_time);

      renderChanges(payload.changes || []);
      renderLine(payload.series || []);
      renderBars(payload.hourly || []);
    }

    function renderChanges(changes) {
      if (!changes.length) {
        els.changes.innerHTML = '<div class="row"><div>暂无余额变化<time>余额保持稳定</time></div><strong>0</strong></div>';
        return;
      }
      els.changes.innerHTML = changes.slice().reverse().slice(0, 8).map(item => {
        const delta = item.delta || 0;
        const cls = delta < 0 ? "down" : "up";
        return '<div class="row"><div>' + money.format(item.from_balance) + ' -> ' + money.format(item.to_balance) +
          '<time>' + dayClock.format(new Date(item.time)) + '</time></div><strong class="' + cls + '">' + signed(delta) + '</strong></div>';
      }).join("");
    }

    function renderLine(series) {
      if (series.length < 1) {
        els.lineEmpty.style.display = "grid";
        els.lineChart.style.display = "none";
        return;
      }
      els.lineEmpty.style.display = "none";
      els.lineChart.style.display = "block";
      const width = 820;
      const height = 318;
      const pad = { left: 54, right: 18, top: 18, bottom: 38 };
      const balances = series.map(p => p.balance);
      const min = Math.min(...balances);
      const max = Math.max(...balances);
      const spread = Math.max(max - min, 0.000001);
      const start = new Date(series[0].time).getTime();
      const end = new Date(series[series.length - 1].time).getTime();
      const span = Math.max(end - start, 1);
      const x = p => pad.left + ((new Date(p.time).getTime() - start) / span) * (width - pad.left - pad.right);
      const y = p => pad.top + ((max - p.balance) / spread) * (height - pad.top - pad.bottom);
      const points = series.map(p => x(p).toFixed(2) + "," + y(p).toFixed(2)).join(" ");
      const area = pad.left + "," + (height - pad.bottom) + " " + points + " " + (width - pad.right) + "," + (height - pad.bottom);
      const grid = [0, .25, .5, .75, 1].map(t => {
        const gy = pad.top + t * (height - pad.top - pad.bottom);
        const value = max - t * spread;
        return '<line x1="' + pad.left + '" y1="' + gy + '" x2="' + (width - pad.right) + '" y2="' + gy + '" stroke="#e7edf3"/>' +
          '<text class="axis" x="8" y="' + (gy + 4) + '">' + compact.format(value) + '</text>';
      }).join("");
      const dots = series.slice(-18).map(p => '<circle cx="' + x(p).toFixed(2) + '" cy="' + y(p).toFixed(2) + '" r="3" fill="#1967d2"/>').join("");
      const labels = '<text class="axis" x="' + pad.left + '" y="' + (height - 12) + '">' + clock.format(new Date(series[0].time)) + '</text>' +
        '<text class="axis" text-anchor="end" x="' + (width - pad.right) + '" y="' + (height - 12) + '">' + clock.format(new Date(series[series.length - 1].time)) + '</text>';
      els.lineChart.setAttribute("viewBox", "0 0 " + width + " " + height);
      els.lineChart.innerHTML = grid +
        '<polygon points="' + area + '" fill="rgba(25, 103, 210, .10)"/>' +
        '<polyline points="' + points + '" fill="none" stroke="#1967d2" stroke-width="4" stroke-linecap="round" stroke-linejoin="round"/>' +
        dots + labels;
    }

    function renderBars(buckets) {
      const width = 360;
      const height = 154;
      const pad = { left: 12, right: 12, top: 16, bottom: 24 };
      els.barChart.setAttribute("viewBox", "0 0 " + width + " " + height);
      if (!buckets.length) {
        els.barChart.innerHTML = '<text class="axis" x="16" y="80">暂无小时聚合</text>';
        return;
      }
      const data = buckets.slice(-24).map(b => ({ ...b, spent: Math.max(0, -(b.change || 0)) }));
      const max = Math.max(...data.map(b => b.spent), 0.000001);
      const slot = (width - pad.left - pad.right) / data.length;
      const bars = data.map((b, i) => {
        const barH = Math.max(2, (b.spent / max) * (height - pad.top - pad.bottom));
        const x = pad.left + i * slot + Math.max(2, slot * .16);
        const y = height - pad.bottom - barH;
        const w = Math.max(4, slot * .68);
        return '<rect x="' + x.toFixed(2) + '" y="' + y.toFixed(2) + '" width="' + w.toFixed(2) + '" height="' + barH.toFixed(2) + '" rx="3" fill="#d93025" opacity=".82"/>';
      }).join("");
      const first = data[0] ? dayClock.format(new Date(data[0].time)) : "";
      const last = data[data.length - 1] ? dayClock.format(new Date(data[data.length - 1].time)) : "";
      els.barChart.innerHTML = bars +
        '<line x1="' + pad.left + '" y1="' + (height - pad.bottom) + '" x2="' + (width - pad.right) + '" y2="' + (height - pad.bottom) + '" stroke="#d9e2ec"/>' +
        '<text class="axis" x="' + pad.left + '" y="' + (height - 6) + '">' + first + '</text>' +
        '<text class="axis" text-anchor="end" x="' + (width - pad.right) + '" y="' + (height - 6) + '">' + last + '</text>';
    }

    function signed(value) {
      const number = Number(value || 0);
      if (Math.abs(number) < 0.000001) return "0";
      return (number > 0 ? "+" : "") + money.format(number);
    }

    function deltaClass(value) {
      const number = Number(value || 0);
      if (Math.abs(number) < 0.000001) return "neutral";
      return number > 0 ? "positive" : "";
    }

    function formatRange(first, last) {
      if (!first || !last) return "暂无有效余额日志";
      return dayClock.format(new Date(first)) + " - " + dayClock.format(new Date(last));
    }

    connect();
  </script>
</body>
</html>`
