package api

const DashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Fraud Operations & Intelligence Studio (FOC)</title>
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <style>
    :root {
      --bg: #0b0f19;
      --card-bg: #151d30;
      --card-bg-subtle: #1e293b;
      --border: #2d3b55;
      --text: #f8fafc;
      --text-muted: #94a3b8;
      --accent-red: #ef4444;
      --accent-yellow: #f59e0b;
      --accent-green: #10b981;
      --accent-blue: #3b82f6;
      --accent-purple: #8b5cf6;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
    body { background-color: var(--bg); color: var(--text); padding: 20px; }
    header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; border-bottom: 1px solid var(--border); padding-bottom: 16px; }
    h1 { font-size: 22px; font-weight: 700; display: flex; align-items: center; gap: 10px; }
    .status-pill { display: inline-flex; align-items: center; gap: 6px; padding: 4px 10px; border-radius: 9999px; font-size: 12px; background: rgba(16, 185, 129, 0.15); color: var(--accent-green); border: 1px solid var(--accent-green); }
    .status-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--accent-green); animation: pulse 2s infinite; }
    @keyframes pulse { 0% { opacity: 1; } 50% { opacity: 0.3; } 100% { opacity: 1; } }

    .tabs { display: flex; gap: 8px; margin-bottom: 16px; border-bottom: 1px solid var(--border); padding-bottom: 8px; }
    .tab-btn { background: none; border: none; color: var(--text-muted); padding: 8px 16px; font-size: 14px; font-weight: 600; cursor: pointer; border-radius: 6px; }
    .tab-btn.active { background: var(--card-bg-subtle); color: var(--text); border: 1px solid var(--border); }

    .grid-main { display: grid; grid-template-columns: 2.2fr 1fr; gap: 20px; }
    .card { background: var(--card-bg); border: 1px solid var(--border); border-radius: 8px; padding: 18px; margin-bottom: 20px; }
    .card h2 { font-size: 16px; margin-bottom: 14px; display: flex; justify-content: space-between; align-items: center; }
    
    .filter-bar { display: flex; gap: 10px; margin-bottom: 14px; }
    select, button, input { background: #0b0f19; border: 1px solid var(--border); color: var(--text); padding: 8px 12px; border-radius: 6px; font-size: 13px; }
    button { cursor: pointer; transition: 0.2s; }
    button:hover { background: #1e293b; border-color: var(--accent-blue); }
    button.btn-primary { background: var(--accent-blue); border: none; }
    button.btn-danger { background: var(--accent-red); border: none; }
    button.btn-success { background: var(--accent-green); border: none; }

    table { width: 100%; border-collapse: collapse; font-size: 13px; }
    th, td { text-align: left; padding: 10px 8px; border-bottom: 1px solid var(--border); }
    th { color: var(--text-muted); font-weight: 600; font-size: 12px; }
    tr:hover td { background: rgba(255, 255, 255, 0.02); cursor: pointer; }

    .badge { padding: 3px 8px; border-radius: 9999px; font-size: 11px; font-weight: 700; text-transform: uppercase; }
    .badge-high { background: rgba(239, 68, 68, 0.2); color: var(--accent-red); border: 1px solid var(--accent-red); }
    .badge-medium { background: rgba(245, 158, 11, 0.2); color: var(--accent-yellow); border: 1px solid var(--accent-yellow); }
    .badge-low { background: rgba(59, 130, 246, 0.2); color: var(--accent-blue); border: 1px solid var(--accent-blue); }

    .badge-APPROVE { background: rgba(16, 185, 129, 0.2); color: var(--accent-green); border: 1px solid var(--accent-green); }
    .badge-CHALLENGE_3DS { background: rgba(245, 158, 11, 0.2); color: var(--accent-yellow); border: 1px solid var(--accent-yellow); }
    .badge-DECLINE { background: rgba(239, 68, 68, 0.2); color: var(--accent-red); border: 1px solid var(--accent-red); }

    .detail-view { display: none; margin-top: 14px; padding: 16px; background: #0b0f19; border-radius: 6px; border: 1px solid var(--border); }
    .signal-item { background: var(--card-bg-subtle); padding: 10px; border-radius: 6px; margin-bottom: 8px; border-left: 4px solid var(--accent-yellow); }
    .signal-item.high { border-left-color: var(--accent-red); }
    pre { background: #020617; padding: 10px; border-radius: 4px; overflow-x: auto; font-size: 12px; margin-top: 6px; }

    /* Graph Visualizer Canvas */
    #graphCanvas { width: 100%; height: 260px; background: #070a12; border-radius: 6px; border: 1px solid var(--border); }
  </style>
</head>
<body>

  <header>
    <div>
      <h1>Fraud Operations & Intelligence Studio (FOC)</h1>
      <p style="color: var(--text-muted); font-size: 13px; margin-top: 4px;">Synchronous Pre-Auth Risk Gate &bull; Graph Syndicate Rings &bull; Closed Retraining Loop &bull; Concept Drift (PSI)</p>
    </div>
    <div style="display: flex; gap: 12px; align-items: center;">
      <div class="status-pill"><div class="status-dot"></div> SLA Budget: &lt;25ms</div>
      <button onclick="refreshAll()" class="btn-primary">↻ Refresh</button>
    </div>
  </header>

  <div class="tabs">
    <button class="tab-btn active" onclick="switchTab('cases')">Cases & Flags</button>
    <button class="tab-btn" onclick="switchTab('authgate')">Pre-Auth Risk Gate (Sandbox)</button>
    <button class="tab-btn" onclick="switchTab('graph')">Entity Graph & Mule Rings</button>
    <button class="tab-btn" onclick="switchTab('drift')">Concept Drift (PSI)</button>
  </div>

  <!-- TAB 1: CASES -->
  <div id="tab-cases">
    <div class="grid-main">
      <div>
        <div class="card">
          <h2>
            <span>Flagged Transactions</span>
            <span id="flag-count" style="font-size: 13px; color: var(--text-muted);">Loading...</span>
          </h2>
          
          <div class="filter-bar">
            <select id="statusFilter" onchange="loadFlags()">
              <option value="">All Statuses</option>
              <option value="open" selected>Open</option>
              <option value="confirmed_fraud">Confirmed Fraud</option>
              <option value="false_positive">False Positive</option>
            </select>
            <select id="severityFilter" onchange="loadFlags()">
              <option value="">All Severities</option>
              <option value="high">High (&ge;0.75)</option>
              <option value="medium">Medium (0.50 - 0.74)</option>
              <option value="low">Low (&lt;0.50)</option>
            </select>
          </div>

          <table>
            <thead>
              <tr>
                <th>Severity</th>
                <th>Score</th>
                <th>Amount</th>
                <th>Merchant / Country</th>
                <th>Status</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody id="flags-tbody">
              <tr><td colspan="6" style="text-align:center; color: var(--text-muted);">No flags found</td></tr>
            </tbody>
          </table>

          <div id="detailPane" class="detail-view">
            <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:10px;">
              <h3 id="detailTitle" style="font-size: 15px;">Flag Details</h3>
              <button onclick="closeDetail()" style="padding: 2px 8px;">✕</button>
            </div>
            <div id="detailContent"></div>
            <div style="margin-top: 14px; display: flex; gap: 10px;">
              <button class="btn-danger" onclick="submitVerdict('confirmed_fraud')">✓ Confirm Fraud</button>
              <button class="btn-success" onclick="submitVerdict('false_positive')">✕ False Positive</button>
              <button onclick="generateSAR()" style="background:#4b5563; border:none;">📄 Generate SAR Filing</button>
            </div>
          </div>
        </div>
      </div>

      <!-- Right Column: Stats -->
      <div>
        <div class="card">
          <h2>Per-Rule Precision Telemetry</h2>
          <table>
            <thead>
              <tr><th>Rule</th><th>Flags</th><th>Precision</th></tr>
            </thead>
            <tbody id="rules-tbody"><tr><td colspan="3" style="text-align:center; color: var(--text-muted);">No data</td></tr></tbody>
          </table>
        </div>

        <div class="card">
          <h2>System Telemetry</h2>
          <div style="font-size: 13px; line-height: 1.8;">
            <div><strong>Detection Mode:</strong> Asynchronous + Pre-Auth Gate</div>
            <div><strong>ML Baseline:</strong> Calibrated Gradient Boosting</div>
            <div><strong>Idempotency:</strong> Distributed Locks & Processed Events</div>
            <div><strong>Deduplication:</strong> Deterministic UUID v5 (SHA-1)</div>
          </div>
        </div>
      </div>
    </div>
  </div>

  <!-- TAB 2: PRE-AUTH RISK GATE SANDBOX -->
  <div id="tab-authgate" style="display:none;">
    <div class="card">
      <h2>Synchronous Pre-Authorization Risk Gate Simulator</h2>
      <p style="color:var(--text-muted); font-size:13px; margin-bottom:16px;">
        Evaluates payments synchronously before balance transfer against a strict <strong>&lt;25ms SLA budget</strong>.
      </p>

      <div style="display:grid; grid-template-columns: repeat(4, 1fr); gap:12px; margin-bottom:16px;">
        <div>
          <label style="font-size:12px; color:var(--text-muted);">Amount (£)</label>
          <input type="number" id="authAmt" value="450.00" style="width:100%;">
        </div>
        <div>
          <label style="font-size:12px; color:var(--text-muted);">Country (ISO)</label>
          <input type="text" id="authCountry" value="JP" style="width:100%;">
        </div>
        <div>
          <label style="font-size:12px; color:var(--text-muted);">Channel</label>
          <select id="authChannel" style="width:100%;">
            <option value="online" selected>online</option>
            <option value="contactless">contactless</option>
            <option value="chip">chip</option>
            <option value="atm">atm</option>
          </select>
        </div>
        <div>
          <label style="font-size:12px; color:var(--text-muted);">Device Fingerprint</label>
          <input type="text" id="authDevice" value="device_farm_001" style="width:100%;">
        </div>
      </div>

      <button onclick="testAuthGate()" class="btn-primary" style="padding:10px 20px; font-weight:600;">⚡ Evaluate Authorization (SLA &lt;25ms)</button>

      <div id="authResult" style="display:none; margin-top:20px; padding:16px; background:#0b0f19; border-radius:6px; border:1px solid var(--border);">
        <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:12px;">
          <div style="display:flex; gap:10px; align-items:center;">
            <span id="authDecisionBadge" class="badge"></span>
            <span id="authScoreText" style="font-weight:700;"></span>
          </div>
          <div id="authLatencyText" style="font-size:13px; color:var(--text-muted);"></div>
        </div>
        <div id="authReasonsList" style="font-size:13px; line-height:1.6;"></div>
      </div>
    </div>
  </div>

  <!-- TAB 3: ENTITY GRAPH & MULE RINGS -->
  <div id="tab-graph" style="display:none;">
    <div class="card">
      <h2>Mule Syndicates & Shared Device Clusters</h2>
      <canvas id="graphCanvas"></canvas>
      <div style="margin-top:16px;">
        <table>
          <thead>
            <tr><th>Syndicate Type</th><th>Shared Entity</th><th>Cluster Size</th><th>Risk Score</th><th>Status</th></tr>
          </thead>
          <tbody id="syndicates-tbody"><tr><td colspan="5" style="text-align:center;">Scanning graph...</td></tr></tbody>
        </table>
      </div>
    </div>
  </div>

  <!-- TAB 4: CONCEPT DRIFT (PSI) -->
  <div id="tab-drift" style="display:none;">
    <div class="card">
      <h2>Population Stability Index (PSI) Drift Monitor</h2>
      <div id="driftReportContent" style="padding:16px; background:#0b0f19; border-radius:6px; border:1px solid var(--border); font-size:14px;">
        Checking distribution drift...
      </div>
    </div>
  </div>

  <script>
    let activeFlag = null;

    function switchTab(tabId) {
      document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
      document.getElementById('tab-cases').style.display = 'none';
      document.getElementById('tab-authgate').style.display = 'none';
      document.getElementById('tab-graph').style.display = 'none';
      document.getElementById('tab-drift').style.display = 'none';

      document.getElementById('tab-' + tabId).style.display = 'block';
      event.target.classList.add('active');

      if (tabId === 'graph') loadGraph();
      if (tabId === 'drift') loadDrift();
    }

    async function loadFlags() {
      const status = document.getElementById('statusFilter').value;
      const severity = document.getElementById('severityFilter').value;
      const url = '/flags?status=' + encodeURIComponent(status) + '&severity=' + encodeURIComponent(severity);
      
      try {
        const res = await fetch(url);
        const data = await res.json();
        const tbody = document.getElementById('flags-tbody');
        document.getElementById('flag-count').innerText = data.length + ' cases';

        if (!data || data.length === 0) {
          tbody.innerHTML = '<tr><td colspan="6" style="text-align:center; color: var(--text-muted);">No flags matching filter</td></tr>';
          return;
        }

        tbody.innerHTML = data.map(f => {
          const amt = f.txn_details && f.txn_details.amount_minor ? '£' + (f.txn_details.amount_minor / 100).toFixed(2) : '-';
          const merch = f.txn_details && f.txn_details.merchant ? f.txn_details.merchant : 'Unknown';
          const country = f.txn_details && f.txn_details.country ? f.txn_details.country : 'Unknown';
          const date = new Date(f.created_at).toLocaleTimeString();
          return '<tr onclick=\'showDetail(' + JSON.stringify(f) + ')\'>' +
            '<td><span class="badge badge-' + f.severity + '">' + f.severity + '</span></td>' +
            '<td><strong>' + f.score.toFixed(2) + '</strong></td>' +
            '<td>' + amt + '</td>' +
            '<td>' + merch + ' (' + country + ')</td>' +
            '<td><span class="badge">' + f.status + '</span></td>' +
            '<td>' + date + '</td>' +
          '</tr>';
        }).join('');
      } catch (err) {
        console.error(err);
      }
    }

    async function loadStats() {
      try {
        const res = await fetch('/stats/rules');
        const stats = await res.json();
        const tbody = document.getElementById('rules-tbody');
        if (!stats || stats.length === 0) {
          tbody.innerHTML = '<tr><td colspan="3" style="text-align:center; color: var(--text-muted);">No data</td></tr>';
          return;
        }
        tbody.innerHTML = stats.map(s => {
          const prec = (s.precision * 100).toFixed(1) + '%';
          return '<tr>' +
            '<td><code>' + s.rule + '</code></td>' +
            '<td>' + s.flags + ' (TP: ' + s.confirmed + ')</td>' +
            '<td><strong>' + prec + '</strong></td>' +
          '</tr>';
        }).join('');
      } catch (err) {
        console.error(err);
      }
    }

    function showDetail(f) {
      activeFlag = f;
      const pane = document.getElementById('detailPane');
      pane.style.display = 'block';
      document.getElementById('detailTitle').innerText = 'Case Audit ' + f.flag_id.substring(0, 8) + '...';

      let sigsHtml = '';
      if (Array.isArray(f.signals)) {
        sigsHtml = f.signals.map(s => {
          return '<div class="signal-item ' + (s.score >= 0.7 ? 'high' : '') + '">' +
            '<div><strong>' + s.rule + '</strong> &bull; Confidence: ' + s.score.toFixed(2) + '</div>' +
            '<div style="font-size:13px; color:#cbd5e1; margin-top:2px;">' + s.reason + '</div>' +
            (s.evidence ? '<pre>' + JSON.stringify(s.evidence, null, 2) + '</pre>' : '') +
          '</div>';
        }).join('');
      }

      document.getElementById('detailContent').innerHTML = 
        '<div style="font-size:13px; margin-bottom:12px;">' +
          '<div><strong>Account ID:</strong> <code>' + f.account_id + '</code></div>' +
          '<div><strong>Transaction ID:</strong> <code>' + f.transaction_id + '</code></div>' +
          '<div><strong>Ruleset:</strong> ' + f.rules_version + (f.model_version ? ' | Model: ' + f.model_version : '') + '</div>' +
        '</div>' +
        '<h4 style="font-size:14px; margin-bottom:8px;">Decomposed Signals</h4>' +
        sigsHtml;
    }

    function closeDetail() {
      document.getElementById('detailPane').style.display = 'none';
      activeFlag = null;
    }

    async function submitVerdict(verdict) {
      if (!activeFlag) return;
      try {
        const res = await fetch('/flags/' + activeFlag.flag_id + '/review', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({verdict: verdict, reviewed_by: 'lead_fraud_analyst'})
        });
        if (res.ok) {
          closeDetail();
          refreshAll();
        }
      } catch (err) {
        alert('Error: ' + err);
      }
    }

    function generateSAR() {
      if (!activeFlag) return;
      const report = "=== SUSPICIOUS ACTIVITY REPORT (SAR) FILING ===\n" +
        "Reference ID: SAR-" + activeFlag.flag_id + "\n" +
        "Subject Account: " + activeFlag.account_id + "\n" +
        "Transaction ID: " + activeFlag.transaction_id + "\n" +
        "Risk Score: " + activeFlag.score.toFixed(2) + " (" + activeFlag.severity.toUpperCase() + ")\n" +
        "Reason Codes: " + JSON.stringify(activeFlag.signals, null, 2) + "\n" +
        "Generated for Compliance Audit: " + new Date().toISOString();
      alert(report);
    }

    async function testAuthGate() {
      const amt = parseFloat(document.getElementById('authAmt').value) * 100;
      const country = document.getElementById('authCountry').value;
      const channel = document.getElementById('authChannel').value;
      const device = document.getElementById('authDevice').value;

      try {
        const res = await fetch('/v1/authorizations/evaluate', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({
            account_id: 'a0000000-0000-0000-0000-000000000001',
            amount_minor: Math.round(amt),
            currency: 'GBP',
            merchant: 'Simulated Merchant',
            country: country,
            channel: channel,
            device_id: device
          })
        });
        const data = await res.json();
        const box = document.getElementById('authResult');
        box.style.display = 'block';

        const badge = document.getElementById('authDecisionBadge');
        badge.className = 'badge badge-' + data.decision;
        badge.innerText = data.decision;

        document.getElementById('authScoreText').innerText = 'Risk Score: ' + data.risk_score.toFixed(2) + ' (' + data.severity.toUpperCase() + ')';
        document.getElementById('authLatencyText').innerText = 'Execution Latency: ' + data.evaluation_ms.toFixed(2) + ' ms';

        let rHtml = '';
        if (data.reasons && data.reasons.length > 0) {
          rHtml = '<strong>Triggered Signals:</strong><ul>' + data.reasons.map(r => '<li>' + r + '</li>').join('') + '</ul>';
        } else {
          rHtml = '<div style="color:var(--accent-green)">✓ Low risk. Immediate authorization granted.</div>';
        }
        document.getElementById('authReasonsList').innerHTML = rHtml;
      } catch (err) {
        alert('Auth Gate error: ' + err);
      }
    }

    async function loadGraph() {
      try {
        const res = await fetch('/v1/graph/syndicates');
        const data = await res.json();
        const tbody = document.getElementById('syndicates-tbody');
        if (!data || data.length === 0) {
          tbody.innerHTML = '<tr><td colspan="5" style="text-align:center;">No active mule syndicates detected</td></tr>';
        } else {
          tbody.innerHTML = data.map(s => {
            return '<tr>' +
              '<td><code>' + s.type + '</code></td>' +
              '<td><code>' + s.shared_entity + '</code></td>' +
              '<td>' + s.cluster_size + ' accounts</td>' +
              '<td><strong>' + s.score.toFixed(2) + '</strong></td>' +
              '<td><span class="badge badge-high">CONFIRMED CLUSTER</span></td>' +
            '</tr>';
          }).join('');
        }
        drawGraphAnimation();
      } catch (err) {
        console.error(err);
      }
    }

    function drawGraphAnimation() {
      const c = document.getElementById('graphCanvas');
      if (!c) return;
      const ctx = c.getContext('2d');
      c.width = c.parentElement.clientWidth - 40;
      c.height = 240;

      ctx.clearRect(0, 0, c.width, c.height);
      const cx = c.width / 2;
      const cy = c.height / 2;

      // Draw central hub
      ctx.beginPath();
      ctx.arc(cx, cy, 14, 0, 2 * Math.PI);
      ctx.fillStyle = '#ef4444';
      ctx.fill();
      ctx.fillStyle = '#fff';
      ctx.font = '11px sans-serif';
      ctx.fillText('Device Syndicate', cx - 44, cy - 20);

      // Draw linked mule accounts
      for (let i = 0; i < 5; i++) {
        const angle = (i * 2 * Math.PI) / 5;
        const x = cx + Math.cos(angle) * 80;
        const y = cy + Math.sin(angle) * 70;

        ctx.beginPath();
        ctx.moveTo(cx, cy);
        ctx.lineTo(x, y);
        ctx.strokeStyle = '#3b82f6';
        ctx.lineWidth = 2;
        ctx.stroke();

        ctx.beginPath();
        ctx.arc(x, y, 8, 0, 2 * Math.PI);
        ctx.fillStyle = '#10b981';
        ctx.fill();
        ctx.fillStyle = '#94a3b8';
        ctx.fillText('Account ' + (i+1), x + 10, y + 4);
      }
    }

    async function loadDrift() {
      try {
        const res = await fetch('/v1/drift');
        const d = await res.json();
        const box = document.getElementById('driftReportContent');
        const color = d.status === 'STABLE' ? 'var(--accent-green)' : 'var(--accent-red)';
        box.innerHTML = 
          '<div><strong>Feature Monitored:</strong> <code>' + d.feature_name + '</code></div>' +
          '<div style="margin-top:8px;"><strong>Population Stability Index (PSI):</strong> ' + d.psi.toFixed(4) + '</div>' +
          '<div style="margin-top:8px;"><strong>Drift Status:</strong> <span style="color:' + color + '; font-weight:700;">' + d.status + '</span></div>' +
          '<div style="margin-top:12px; font-size:13px; color:var(--text-muted);">' + d.message + '</div>';
      } catch (err) {
        console.error(err);
      }
    }

    function refreshAll() {
      loadFlags();
      loadStats();
    }

    refreshAll();
  </script>
</body>
</html>
`
