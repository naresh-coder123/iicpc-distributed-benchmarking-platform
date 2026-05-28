import React, { useCallback, useEffect, useRef, useState } from 'react';

// ── API base URLs (client-only to avoid SSR hydration mismatch) ───────────────
function resolveLeaderboardBase() {
  if (process.env.NEXT_PUBLIC_API_BASE) return process.env.NEXT_PUBLIC_API_BASE;
  return `http://${window.location.hostname}:30080`;
}
function resolveJudgeBase() {
  if (process.env.NEXT_PUBLIC_JUDGE_BASE) return process.env.NEXT_PUBLIC_JUDGE_BASE;
  return `http://${window.location.hostname}:30081`;
}
function useApiBases() {
  const [bases, setBases] = useState(null);
  useEffect(() => {
    setBases({ lBase: resolveLeaderboardBase(), jBase: resolveJudgeBase() });
  }, []);
  return bases;
}

// ── Error Boundary ────────────────────────────────────────────────────────────
class ErrorBoundary extends React.Component {
  constructor(props) { super(props); this.state = { hasError: false, error: null }; }
  static getDerivedStateFromError(e) { return { hasError: true, error: e }; }
  render() {
    if (this.state.hasError) return (
      <div style={{ padding: 24, background: '#fff3f3', border: '1px solid #ffd1d1', borderRadius: 4 }}>
        <b>Something went wrong.</b>
        <pre style={{ fontSize: 12, marginTop: 8, whiteSpace: 'pre-wrap' }}>{String(this.state.error)}</pre>
        <button style={btnStyle} onClick={() => this.setState({ hasError: false, error: null })}>Try again</button>
      </div>
    );
    return this.props.children;
  }
}

// ── Shared helpers ────────────────────────────────────────────────────────────
async function apiFetch(url, opts = {}) {
  const res = await fetch(url, opts);
  const text = await res.text();
  let data; try { data = JSON.parse(text); } catch { data = text; }
  if (!res.ok) throw new Error(data?.error || text || res.statusText);
  return data;
}
function Spinner() { return <span style={{ marginLeft: 8, color: '#888', fontSize: 13 }}>Loading…</span>; }
function ErrorBox({ msg }) {
  return <div style={{ padding: 10, background: '#fff3f3', border: '1px solid #ffd1d1', borderRadius: 4, marginBottom: 12, fontSize: 13 }}><b>Error:</b> {msg}</div>;
}
function statusColor(s) {
  if (s === 'COMPLETED') return { color: '#0a7c42', fontWeight: 700 };
  if (s === 'RUNNING')   return { color: '#b45309', fontWeight: 700 };
  if (s === 'FAILED')    return { color: '#c00',    fontWeight: 700 };
  return { color: '#555' };
}

// ── Tabs ──────────────────────────────────────────────────────────────────────
const TABS = ['Dashboard', 'Leaderboard', 'Submit', 'My Runs', 'History', 'Contestants'];

export default function Home() {
  const [tab, setTab] = useState('Dashboard');
  const bases = useApiBases();
  if (!bases) return (
    <main style={{ fontFamily: 'system-ui, Arial', padding: 24 }}>
      <h1 style={{ marginBottom: 4 }}>IICPC — Trading Engine Challenge</h1>
      <p style={{ color: '#888', fontSize: 13 }}>Initialising…</p>
    </main>
  );
  const { lBase, jBase } = bases;
  return (
    <ErrorBoundary>
      <main style={{ fontFamily: 'system-ui, Arial', padding: 24, maxWidth: 1200 }}>
        <h1 style={{ marginBottom: 4 }}>IICPC — Trading Engine Challenge</h1>
        <p style={{ marginTop: 0, color: '#555', fontSize: 12 }}>
          Leaderboard: <code>{lBase}</code> &nbsp;|&nbsp; Judge: <code>{jBase}</code>
        </p>
        <div style={{ display: 'flex', gap: 4, marginBottom: 20, borderBottom: '2px solid #e0e0e0' }}>
          {TABS.map(t => (
            <button key={t} onClick={() => setTab(t)} style={{
              padding: '8px 16px', border: 'none',
              borderBottom: tab === t ? '2px solid #0070f3' : '2px solid transparent',
              background: 'none', cursor: 'pointer',
              fontWeight: tab === t ? 700 : 400,
              color: tab === t ? '#0070f3' : '#333',
              fontSize: 14, marginBottom: -2,
            }}>{t}</button>
          ))}
        </div>
        <ErrorBoundary>
          {tab === 'Dashboard'   && <DashboardTab lBase={lBase} jBase={jBase} />}
          {tab === 'Leaderboard' && <LeaderboardTab lBase={lBase} />}
          {tab === 'Submit'      && <SubmitTab jBase={jBase} />}
          {tab === 'My Runs'     && <MyRunsTab jBase={jBase} />}
          {tab === 'History'     && <HistoryTab lBase={lBase} />}
          {tab === 'Contestants' && <ContestantsTab jBase={jBase} />}
        </ErrorBoundary>
      </main>
    </ErrorBoundary>
  );
}

// ── Dashboard tab — live graphs + sandbox status + log terminal ───────────────
function DashboardTab({ lBase, jBase }) {
  const [rows, setRows]         = useState([]);
  const [queueStatus, setQueue] = useState(null);
  const [logs, setLogs]         = useState([]);       // diagnostic log entries
  const [tpsHistory, setTps]    = useState([]);       // [{t, tps, contestant}]
  const [latHistory, setLat]    = useState([]);       // [{t, p50, p99, contestant}]
  const logsRef                 = useRef(null);
  const prevRowsRef             = useRef({});

  const addLog = useCallback((msg) => {
    const ts = new Date().toLocaleTimeString();
    setLogs(l => [...l.slice(-199), `[${ts}] ${msg}`]);
  }, []);

  // Fetch leaderboard + queue status every second.
  useEffect(() => {
    let alive = true;
    async function tick() {
      if (!alive) return;
      try {
        const data = await apiFetch(`${lBase}/leaderboard?limit=20`);
        if (!Array.isArray(data)) return;
        setRows(data);

        // Detect score changes for log entries.
        data.forEach(r => {
          const prev = prevRowsRef.current[r.contestant_id];
          const score = Number(r.score || 0).toFixed(0);
          if (prev !== score) {
            addLog(`Score update: ${r.contestant_id} → ${score} (p99=${r.p99_us}µs, correct=${(Number(r.correct_ratio||0)*100).toFixed(1)}%)`);
            prevRowsRef.current[r.contestant_id] = score;
          }
          // Append to sparkline history (keep last 60 points).
          const t = Date.now();
          setTps(h => [...h.slice(-59), { t, tps: Number(r.sustained_tps || 0), contestant: r.contestant_id }]);
          setLat(h => [...h.slice(-59), { t, p50: Number(r.p50_us || 0), p99: Number(r.p99_us || 0), contestant: r.contestant_id }]);
        });
      } catch (e) { addLog(`Leaderboard fetch error: ${e.message}`); }

      try {
        const q = await apiFetch(`${jBase}/admin/queue`);
        setQueue(q);
      } catch { /* judge may not be running */ }
    }
    tick();
    const id = setInterval(tick, 1000);
    return () => { alive = false; clearInterval(id); };
  }, [lBase, jBase, addLog]);

  // SSE for instant updates.
  useEffect(() => {
    const es = new EventSource(`${lBase}/stream`);
    es.addEventListener('update', e => addLog(`SSE update: contestant=${e.data}`));
    es.addEventListener('error', () => addLog('SSE disconnected — reconnecting…'));
    return () => es.close();
  }, [lBase, addLog]);

  // Auto-scroll log terminal.
  useEffect(() => {
    if (logsRef.current) logsRef.current.scrollTop = logsRef.current.scrollHeight;
  }, [logs]);

  // Build simple SVG sparklines from history arrays.
  const topContestants = [...new Set(rows.map(r => r.contestant_id))].slice(0, 5);
  const COLORS = ['#0070f3','#e63946','#2a9d8f','#e9c46a','#f4a261'];

  return (
    <section>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 16 }}>
        {/* TPS Sparkline */}
        <div style={cardStyle}>
          <h3 style={cardTitle}>Sustained TPS</h3>
          <Sparkline data={tpsHistory} contestants={topContestants} colors={COLORS} valueKey="tps" />
        </div>
        {/* p99 Latency Sparkline */}
        <div style={cardStyle}>
          <h3 style={cardTitle}>p99 Latency (µs)</h3>
          <Sparkline data={latHistory} contestants={topContestants} colors={COLORS} valueKey="p99" />
        </div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 16 }}>
        {/* Sandbox / Queue Status */}
        <div style={cardStyle}>
          <h3 style={cardTitle}>Sandbox Status</h3>
          {queueStatus ? (
            <table style={{ fontSize: 13, width: '100%' }}>
              <tbody>
                <tr><td style={statLabel}>Queue pending</td><td style={statVal}>{queueStatus.pending}</td></tr>
                <tr><td style={statLabel}>Running</td><td style={statVal}>{queueStatus.running}</td></tr>
              </tbody>
            </table>
          ) : <p style={{ color: '#888', fontSize: 13 }}>Judge API not reachable</p>}
          <div style={{ marginTop: 8 }}>
            {rows.slice(0, 5).map((r, i) => (
              <div key={r.contestant_id} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                <span style={{ width: 10, height: 10, borderRadius: '50%', background: COLORS[i % COLORS.length], display: 'inline-block' }} />
                <span style={{ fontSize: 12, flex: 1 }}>{r.contestant_id}</span>
                <span style={{ fontSize: 12, color: '#0070f3', fontWeight: 700 }}>{Number(r.score||0).toFixed(0)}</span>
              </div>
            ))}
          </div>
        </div>

        {/* Score breakdown for top contestant */}
        <div style={cardStyle}>
          <h3 style={cardTitle}>Score Breakdown (top contestant)</h3>
          {rows[0] ? <ScoreBreakdown row={rows[0]} /> : <p style={{ color: '#888', fontSize: 13 }}>No data yet</p>}
        </div>
      </div>

      {/* Diagnostic log terminal */}
      <div style={cardStyle}>
        <h3 style={cardTitle}>Diagnostic Log</h3>
        <div ref={logsRef} style={{
          background: '#0d1117', color: '#c9d1d9', fontFamily: 'monospace',
          fontSize: 11, padding: 10, height: 180, overflowY: 'auto',
          borderRadius: 4, lineHeight: 1.6,
        }}>
          {logs.length === 0
            ? <span style={{ color: '#555' }}>Waiting for events…</span>
            : logs.map((l, i) => <div key={i}>{l}</div>)
          }
        </div>
      </div>
    </section>
  );
}

// ── Sparkline SVG component ───────────────────────────────────────────────────
function Sparkline({ data, contestants, colors, valueKey }) {
  const W = 400, H = 80, PAD = 4;
  if (data.length < 2) return <div style={{ height: H, color: '#888', fontSize: 12, display: 'flex', alignItems: 'center' }}>Collecting data…</div>;

  // One line per contestant.
  const lines = contestants.map((cid, ci) => {
    const pts = data.filter(d => d.contestant === cid);
    if (pts.length < 2) return null;
    const vals = pts.map(d => d[valueKey]);
    const maxV = Math.max(...vals, 1);
    const minV = Math.min(...vals, 0);
    const range = maxV - minV || 1;
    const xs = pts.map((_, i) => PAD + (i / (pts.length - 1)) * (W - 2 * PAD));
    const ys = vals.map(v => H - PAD - ((v - minV) / range) * (H - 2 * PAD));
    const d = xs.map((x, i) => `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${ys[i].toFixed(1)}`).join(' ');
    return <path key={cid} d={d} fill="none" stroke={colors[ci % colors.length]} strokeWidth="1.5" />;
  }).filter(Boolean);

  const allVals = data.map(d => d[valueKey]);
  const maxV = Math.max(...allVals, 1);

  return (
    <div>
      <svg width="100%" viewBox={`0 0 ${W} ${H}`} style={{ display: 'block' }}>
        {/* Grid lines */}
        {[0.25, 0.5, 0.75].map(f => (
          <line key={f} x1={PAD} y1={H * f} x2={W - PAD} y2={H * f}
            stroke="#e0e0e0" strokeWidth="0.5" strokeDasharray="3,3" />
        ))}
        {lines}
      </svg>
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', marginTop: 4 }}>
        {contestants.map((cid, i) => (
          <span key={cid} style={{ fontSize: 11, display: 'flex', alignItems: 'center', gap: 4 }}>
            <span style={{ width: 12, height: 3, background: colors[i % colors.length], display: 'inline-block', borderRadius: 2 }} />
            {cid}
          </span>
        ))}
        <span style={{ fontSize: 11, color: '#888', marginLeft: 'auto' }}>max: {maxV.toFixed(0)}</span>
      </div>
    </div>
  );
}

// ── Score breakdown card ──────────────────────────────────────────────────────
function ScoreBreakdown({ row }) {
  const sL = Number(row.score_latency    || 0).toFixed(1);
  const sT = Number(row.score_throughput || 0).toFixed(1);
  const sC = Number(row.score_correctness|| 0).toFixed(1);
  const total = Number(row.score || 0).toFixed(1);
  const bar = (v, max, color) => (
    <div style={{ background: '#f0f0f0', borderRadius: 3, height: 10, flex: 1 }}>
      <div style={{ width: `${Math.min(100, (v/max)*100).toFixed(1)}%`, background: color, height: '100%', borderRadius: 3 }} />
    </div>
  );
  return (
    <table style={{ fontSize: 13, width: '100%', borderCollapse: 'collapse' }}>
      <tbody>
        <tr>
          <td style={statLabel}>S_L (latency, 40%)</td>
          <td style={{ width: 120, paddingRight: 8 }}>{bar(sL, 100, '#0070f3')}</td>
          <td style={statVal}>{sL}</td>
        </tr>
        <tr>
          <td style={statLabel}>S_T (throughput, 30%)</td>
          <td>{bar(sT, 100, '#2a9d8f')}</td>
          <td style={statVal}>{sT}</td>
        </tr>
        <tr>
          <td style={statLabel}>S_C (correctness, 30%)</td>
          <td>{bar(sC, 100, '#e9c46a')}</td>
          <td style={statVal}>{sC}</td>
        </tr>
        <tr style={{ borderTop: '1px solid #ddd' }}>
          <td style={{ ...statLabel, fontWeight: 700 }}>S_Total</td>
          <td>{bar(total, 100, '#0070f3')}</td>
          <td style={{ ...statVal, fontWeight: 700, color: '#0070f3' }}>{total}</td>
        </tr>
      </tbody>
    </table>
  );
}

// ── Leaderboard tab ───────────────────────────────────────────────────────────
function LeaderboardTab({ lBase }) {
  const [rows, setRows]       = useState([]);
  const [err, setErr]         = useState('');
  const [loading, setLoading] = useState(false);
  const timerRef              = useRef(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      setErr('');
      const data = await apiFetch(`${lBase}/leaderboard?limit=50`);
      setRows(Array.isArray(data) ? data : []);
    } catch (e) { setErr(String(e?.message || e)); }
    finally { setLoading(false); }
  }, [lBase]);

  useEffect(() => {
    refresh();
    const es = new EventSource(`${lBase}/stream`);
    es.addEventListener('update', () => {
      if (timerRef.current) clearTimeout(timerRef.current);
      timerRef.current = setTimeout(refresh, 200);
    });
    es.addEventListener('error', () => setErr('SSE disconnected — reconnecting…'));
    return () => { es.close(); if (timerRef.current) clearTimeout(timerRef.current); };
  }, [lBase, refresh]);

  return (
    <section>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12 }}>
        <h2 style={{ margin: 0 }}>Live Leaderboard</h2>
        <button onClick={refresh} style={btnStyle} disabled={loading}>Refresh</button>
        {loading && <Spinner />}
      </div>
      {err && <ErrorBox msg={err} />}
      <table style={tableStyle}>
        <thead><tr>
          {['Rank','Contestant','Orders','TPS','Correct%','p50µs','p90µs','p99µs','S_L','S_T','S_C','Score'].map(h => (
            <th key={h} style={th}>{h}</th>
          ))}
        </tr></thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={r.contestant_id || i} style={i % 2 === 0 ? {} : { background: '#fafafa' }}>
              <td style={td}><b>{i + 1}</b></td>
              <td style={td}>{r.contestant_id}</td>
              <td style={td}>{r.count}</td>
              <td style={td}>{Number(r.sustained_tps || 0).toFixed(0)}</td>
              <td style={td}>{(Number(r.correct_ratio || 0) * 100).toFixed(2)}%</td>
              <td style={td}>{r.p50_us}</td>
              <td style={td}>{r.p90_us}</td>
              <td style={td}>{r.p99_us}</td>
              <td style={td}>{Number(r.score_latency     || 0).toFixed(1)}</td>
              <td style={td}>{Number(r.score_throughput  || 0).toFixed(1)}</td>
              <td style={td}>{Number(r.score_correctness || 0).toFixed(1)}</td>
              <td style={{ ...td, fontWeight: 700, color: '#0070f3' }}>{Number(r.score || 0).toFixed(1)}</td>
            </tr>
          ))}
          {rows.length === 0 && !loading && (
            <tr><td style={td} colSpan={12}>No data yet. Submit an engine to get started.</td></tr>
          )}
        </tbody>
      </table>
    </section>
  );
}

// ── Submit tab ────────────────────────────────────────────────────────────────
function SubmitTab({ jBase }) {
  const [contestantId, setContestantId] = useState('');
  const [imageTag, setImageTag]         = useState('');
  const [result, setResult]             = useState(null);
  const [err, setErr]                   = useState('');
  const [loading, setLoading]           = useState(false);

  async function submit(e) {
    e.preventDefault(); setErr(''); setResult(null); setLoading(true);
    try {
      const data = await apiFetch(`${jBase}/submissions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ contestant_id: contestantId, image_tag: imageTag }),
      });
      setResult(data);
    } catch (ex) { setErr(String(ex?.message || ex)); }
    finally { setLoading(false); }
  }

  return (
    <section>
      <h2>Submit Your Engine</h2>
      <p style={{ color: '#555', fontSize: 13 }}>
        Your image must implement <code>TradingGateway</code> gRPC on port <code>50051</code>.
        The judge validates the engine, then fires a bot fleet (60% MARKET, 30% LIMIT, 10% CANCEL probes).
      </p>
      <form onSubmit={submit} style={{ display: 'flex', flexDirection: 'column', gap: 12, maxWidth: 480 }}>
        <label style={labelStyle}>Contestant ID<input style={inputStyle} value={contestantId} onChange={e => setContestantId(e.target.value)} placeholder="team-alpha" required /></label>
        <label style={labelStyle}>Docker Image Tag<input style={inputStyle} value={imageTag} onChange={e => setImageTag(e.target.value)} placeholder="registry/engine:v1" required /></label>
        <button type="submit" disabled={loading} style={{ ...btnStyle, alignSelf: 'flex-start', padding: '8px 24px' }}>
          {loading ? 'Submitting…' : 'Submit'}
        </button>
      </form>
      {err && <ErrorBox msg={err} />}
      {result && (
        <div style={{ marginTop: 16, padding: 12, background: '#f0fff4', border: '1px solid #b2f5c8', borderRadius: 4 }}>
          <b>Accepted</b> — switch to <b>My Runs</b> to track progress.
          <pre style={{ margin: '8px 0 0', fontSize: 12 }}>{JSON.stringify(result, null, 2)}</pre>
        </div>
      )}
    </section>
  );
}

// ── My Runs tab ───────────────────────────────────────────────────────────────
function MyRunsTab({ jBase }) {
  const [contestantId, setContestantId] = useState('');
  const [runs, setRuns]                 = useState(null);
  const [err, setErr]                   = useState('');
  const [loading, setLoading]           = useState(false);
  const intervalRef                     = useRef(null);

  const fetchRuns = useCallback(async (id) => {
    if (!id) return; setErr('');
    try {
      const data = await apiFetch(`${jBase}/contestants/${encodeURIComponent(id)}/runs`);
      setRuns(Array.isArray(data) ? data : []);
    } catch (ex) { setErr(String(ex?.message || ex)); setRuns(null); }
  }, [jBase]);

  useEffect(() => {
    if (!runs) return;
    const hasRunning = runs.some(r => r.status === 'RUNNING');
    if (hasRunning) { intervalRef.current = setInterval(() => fetchRuns(contestantId), 3000); }
    return () => clearInterval(intervalRef.current);
  }, [runs, contestantId, fetchRuns]);

  async function handleSubmit(e) {
    e.preventDefault(); setLoading(true); await fetchRuns(contestantId); setLoading(false);
  }

  return (
    <section>
      <h2>My Runs</h2>
      <form onSubmit={handleSubmit} style={{ display: 'flex', gap: 8, alignItems: 'flex-end', marginBottom: 16 }}>
        <label style={labelStyle}>Contestant ID
          <input style={{ ...inputStyle, width: 240 }} value={contestantId} onChange={e => setContestantId(e.target.value)} placeholder="team-alpha" required />
        </label>
        <button type="submit" disabled={loading} style={btnStyle}>{loading ? 'Loading…' : 'Load Runs'}</button>
        {loading && <Spinner />}
      </form>
      {err && <ErrorBox msg={err} />}
      {runs !== null && (runs.length === 0
        ? <p style={{ color: '#888' }}>No runs found.</p>
        : <table style={tableStyle}><thead><tr>
            {['Run ID','Status','Started','Duration','Orders','Correct%','p50µs','p99µs','Score','Error'].map(h => <th key={h} style={th}>{h}</th>)}
          </tr></thead><tbody>
          {runs.map((r, i) => {
            const dur = r.finished_at ? ((new Date(r.finished_at) - new Date(r.started_at)) / 1000).toFixed(1) + 's' : '—';
            return <tr key={r.run_id} style={i % 2 === 0 ? {} : { background: '#fafafa' }}>
              <td style={{ ...td, fontSize: 11, fontFamily: 'monospace' }}>{r.run_id}</td>
              <td style={{ ...td, ...statusColor(r.status) }}>{r.status}</td>
              <td style={{ ...td, fontSize: 11 }}>{r.started_at ? new Date(r.started_at).toLocaleTimeString() : '—'}</td>
              <td style={td}>{dur}</td>
              <td style={td}>{r.total_orders || '—'}</td>
              <td style={td}>{r.correct_ratio != null ? (r.correct_ratio * 100).toFixed(2) + '%' : '—'}</td>
              <td style={td}>{r.p50_us || '—'}</td>
              <td style={td}>{r.p99_us || '—'}</td>
              <td style={{ ...td, fontWeight: 700, color: '#0070f3' }}>{r.score != null ? Number(r.score).toFixed(1) : '—'}</td>
              <td style={{ ...td, fontSize: 11, color: '#c00', maxWidth: 180, wordBreak: 'break-all' }}>{r.error || ''}</td>
            </tr>;
          })}
        </tbody></table>
      )}
    </section>
  );
}

// ── History tab ───────────────────────────────────────────────────────────────
function HistoryTab({ lBase }) {
  const [contestantId, setContestantId] = useState('');
  const [hours, setHours]               = useState('1');
  const [data, setData]                 = useState(null);
  const [err, setErr]                   = useState('');
  const [loading, setLoading]           = useState(false);

  async function fetchHistory(e) {
    e?.preventDefault(); if (!contestantId) return;
    setErr(''); setLoading(true);
    try {
      const result = await apiFetch(`${lBase}/contestants/${encodeURIComponent(contestantId)}/history?hours=${hours}`);
      setData(Array.isArray(result) ? result : []);
    } catch (ex) { setErr(String(ex?.message || ex)); setData(null); }
    finally { setLoading(false); }
  }

  return (
    <section>
      <h2>Performance History</h2>
      <form onSubmit={fetchHistory} style={{ display: 'flex', gap: 8, alignItems: 'flex-end', marginBottom: 16, flexWrap: 'wrap' }}>
        <label style={labelStyle}>Contestant ID<input style={{ ...inputStyle, width: 220 }} value={contestantId} onChange={e => setContestantId(e.target.value)} placeholder="team-alpha" required /></label>
        <label style={labelStyle}>Window
          <select style={{ ...inputStyle, width: 130 }} value={hours} onChange={e => setHours(e.target.value)}>
            <option value="1">Last 1 hour</option>
            <option value="6">Last 6 hours</option>
            <option value="24">Last 24 hours</option>
            <option value="48">Last 48 hours</option>
          </select>
        </label>
        <button type="submit" disabled={loading} style={btnStyle}>{loading ? 'Loading…' : 'Load'}</button>
        {loading && <Spinner />}
      </form>
      {err && <ErrorBox msg={err} />}
      {data !== null && (data.length === 0
        ? <p style={{ color: '#888' }}>No history found.</p>
        : <table style={tableStyle}><thead><tr>
            {['Window','Orders','Correct%','p50µs','p90µs','p99µs','Avg µs','Min µs','Max µs'].map(h => <th key={h} style={th}>{h}</th>)}
          </tr></thead><tbody>
          {data.map((row, i) => (
            <tr key={i} style={i % 2 === 0 ? {} : { background: '#fafafa' }}>
              <td style={{ ...td, fontSize: 11, fontFamily: 'monospace' }}>{row.window_start ? new Date(row.window_start).toLocaleTimeString() : '—'}</td>
              <td style={td}>{row.count}</td>
              <td style={td}>{row.correct_ratio != null ? (row.correct_ratio * 100).toFixed(2) + '%' : '—'}</td>
              <td style={td}>{row.p50_us}</td>
              <td style={td}>{row.p90_us}</td>
              <td style={{ ...td, fontWeight: 600 }}>{row.p99_us}</td>
              <td style={td}>{row.avg_us}</td>
              <td style={td}>{row.min_us}</td>
              <td style={td}>{row.max_us}</td>
            </tr>
          ))}
        </tbody></table>
      )}
    </section>
  );
}

// ── Contestants tab ───────────────────────────────────────────────────────────
function ContestantsTab({ jBase }) {
  const [contestants, setContestants] = useState(null);
  const [err, setErr]                 = useState('');
  const [listLoading, setListLoading] = useState(false);
  const [regId, setRegId]             = useState('');
  const [regName, setRegName]         = useState('');
  const [regResult, setRegResult]     = useState(null);
  const [regErr, setRegErr]           = useState('');
  const [regLoading, setRegLoading]   = useState(false);

  const loadContestants = useCallback(async () => {
    setErr(''); setListLoading(true);
    try { const data = await apiFetch(`${jBase}/contestants`); setContestants(Array.isArray(data) ? data : []); }
    catch (ex) { setErr(String(ex?.message || ex)); }
    finally { setListLoading(false); }
  }, [jBase]);

  useEffect(() => { loadContestants(); }, [loadContestants]);

  async function register(e) {
    e.preventDefault(); setRegErr(''); setRegResult(null); setRegLoading(true);
    try {
      const data = await apiFetch(`${jBase}/contestants`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: regId, display_name: regName }),
      });
      setRegResult(data); setRegId(''); setRegName(''); loadContestants();
    } catch (ex) { setRegErr(String(ex?.message || ex)); }
    finally { setRegLoading(false); }
  }

  return (
    <section>
      <h2>Register Contestant</h2>
      <form onSubmit={register} style={{ display: 'flex', gap: 8, alignItems: 'flex-end', marginBottom: 16, flexWrap: 'wrap' }}>
        <label style={labelStyle}>ID (slug)<input style={{ ...inputStyle, width: 180 }} value={regId} onChange={e => setRegId(e.target.value)} placeholder="team-alpha" required /></label>
        <label style={labelStyle}>Display Name<input style={{ ...inputStyle, width: 220 }} value={regName} onChange={e => setRegName(e.target.value)} placeholder="Team Alpha" required /></label>
        <button type="submit" disabled={regLoading} style={btnStyle}>{regLoading ? 'Registering…' : 'Register'}</button>
      </form>
      {regErr    && <ErrorBox msg={regErr} />}
      {regResult && <div style={{ padding: 8, background: '#f0fff4', border: '1px solid #b2f5c8', borderRadius: 4, marginBottom: 12, fontSize: 13 }}>Registered: <b>{regResult.display_name}</b> ({regResult.id})</div>}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 8 }}>
        <h2 style={{ margin: 0 }}>All Contestants</h2>
        <button onClick={loadContestants} style={btnStyle} disabled={listLoading}>Refresh</button>
        {listLoading && <Spinner />}
      </div>
      {err && <ErrorBox msg={err} />}
      {contestants !== null && (contestants.length === 0
        ? <p style={{ color: '#888' }}>No contestants registered yet.</p>
        : <table style={tableStyle}><thead><tr>{['ID','Display Name','Registered At'].map(h => <th key={h} style={th}>{h}</th>)}</tr></thead>
          <tbody>{contestants.map((c, i) => (
            <tr key={c.id} style={i % 2 === 0 ? {} : { background: '#fafafa' }}>
              <td style={{ ...td, fontFamily: 'monospace' }}>{c.id}</td>
              <td style={td}>{c.display_name}</td>
              <td style={{ ...td, fontSize: 12, color: '#888' }}>{c.registered_at ? new Date(c.registered_at).toLocaleString() : '—'}</td>
            </tr>
          ))}</tbody></table>
      )}
    </section>
  );
}

// ── Shared styles ─────────────────────────────────────────────────────────────
const tableStyle = { width: '100%', borderCollapse: 'collapse', fontSize: 13 };
const th = { textAlign: 'left', borderBottom: '2px solid #ddd', padding: '8px 6px', whiteSpace: 'nowrap' };
const td = { borderBottom: '1px solid #eee', padding: '7px 6px', verticalAlign: 'top' };
const btnStyle = { padding: '6px 14px', background: '#0070f3', color: '#fff', border: 'none', borderRadius: 4, cursor: 'pointer', fontSize: 13 };
const inputStyle = { display: 'block', marginTop: 4, padding: '6px 8px', border: '1px solid #ccc', borderRadius: 4, fontSize: 13, width: '100%' };
const labelStyle = { fontSize: 13, fontWeight: 600, color: '#333' };
const cardStyle = { background: '#fff', border: '1px solid #e0e0e0', borderRadius: 6, padding: 16 };
const cardTitle = { margin: '0 0 12px', fontSize: 14, fontWeight: 700, color: '#333' };
const statLabel = { fontSize: 13, color: '#555', padding: '4px 8px 4px 0', whiteSpace: 'nowrap' };
const statVal   = { fontSize: 13, fontWeight: 700, color: '#0070f3', textAlign: 'right', paddingLeft: 8 };
