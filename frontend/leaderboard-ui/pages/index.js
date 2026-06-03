import React, { useCallback, useEffect, useRef, useState } from 'react';
import Head from 'next/head';

// ── API base URLs (client-only to avoid SSR hydration mismatch) ──────────────
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
      <div className="alert alert-error" style={{ margin: '24px 0' }}>
        <span>⚠</span>
        <div>
          <strong>Something went wrong.</strong>
          <pre style={{ fontSize: '0.6875rem', marginTop: 6, whiteSpace: 'pre-wrap', opacity: 0.8 }}>{String(this.state.error)}</pre>
          <button className="btn btn-ghost" style={{ marginTop: 10 }} onClick={() => this.setState({ hasError: false, error: null })}>Try again</button>
        </div>
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

function Spinner() { return <span className="spinner" aria-label="Loading" />; }

function ErrorBox({ msg }) {
  return <div className="alert alert-error" style={{ marginBottom: 16 }}><span>✕</span><span>{msg}</span></div>;
}

function SuccessBox({ children }) {
  return <div className="alert alert-success" style={{ marginBottom: 16 }}><span>✓</span><div>{children}</div></div>;
}

function statusBadge(s) {
  const map = { COMPLETED: 'badge-completed', RUNNING: 'badge-running', FAILED: 'badge-failed', QUEUED: 'badge-queued' };
  return <span className={`badge ${map[s] || 'badge-queued'}`}>{s || '—'}</span>;
}

const PALETTE = ['#38bdf8','#818cf8','#34d399','#fbbf24','#fb7185','#a78bfa'];
const TABS = [
  { id: 'Dashboard',    icon: '⚡', label: 'Dashboard'   },
  { id: 'Leaderboard', icon: '🏆', label: 'Leaderboard'  },
  { id: 'Submit',      icon: '🚀', label: 'Submit'       },
  { id: 'My Runs',     icon: '📊', label: 'My Runs'      },
  { id: 'History',     icon: '📈', label: 'History'      },
  { id: 'Contestants', icon: '👥', label: 'Contestants'  },
];

// ── Root Page ─────────────────────────────────────────────────────────────────
export default function Home() {
  const [tab, setTab] = useState('Dashboard');
  const bases = useApiBases();

  return (
    <>
      <Head>
        <title>IICPC 2026 — Trading Engine Challenge</title>
      </Head>

      {/* Header */}
      <header className="app-header">
        <div className="header-inner">
          <div className="header-brand">
            <div className="header-logo" aria-hidden="true">⚡</div>
            <div>
              <div className="header-title">IICPC 2026</div>
              <div className="header-subtitle">Distributed Benchmarking Platform</div>
            </div>
          </div>
          {bases && (
            <div className="header-meta">
              <span className="api-badge" title="Leaderboard API">LB {new URL(bases.lBase).host}</span>
              <span className="api-badge" title="Judge API" style={{ borderColor: 'rgba(129,140,248,0.25)', color: 'var(--accent-violet)' }}>
                JD {new URL(bases.jBase).host}
              </span>
            </div>
          )}
        </div>
      </header>

      <main className="app-shell" style={{ paddingTop: 28 }}>
        {/* Nav tabs */}
        <nav className="tab-bar" role="tablist" aria-label="Main navigation">
          {TABS.map(t => (
            <button
              key={t.id}
              id={`tab-${t.id.replace(' ', '-').toLowerCase()}`}
              role="tab"
              aria-selected={tab === t.id}
              className={`tab-btn${tab === t.id ? ' active' : ''}`}
              onClick={() => setTab(t.id)}
            >
              <span aria-hidden="true">{t.icon}</span>
              {t.label}
            </button>
          ))}
        </nav>

        {/* Tab content */}
        {!bases ? (
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, color: 'var(--text-muted)', padding: '40px 0' }}>
            <Spinner /> <span>Initialising…</span>
          </div>
        ) : (
          <ErrorBoundary>
            <div className="tab-content" key={tab}>
              {tab === 'Dashboard'   && <DashboardTab   lBase={bases.lBase} jBase={bases.jBase} />}
              {tab === 'Leaderboard' && <LeaderboardTab lBase={bases.lBase} />}
              {tab === 'Submit'      && <SubmitTab      jBase={bases.jBase} />}
              {tab === 'My Runs'     && <MyRunsTab      jBase={bases.jBase} />}
              {tab === 'History'     && <HistoryTab     lBase={bases.lBase} />}
              {tab === 'Contestants' && <ContestantsTab jBase={bases.jBase} />}
            </div>
          </ErrorBoundary>
        )}
      </main>
    </>
  );
}

// ── Dashboard ─────────────────────────────────────────────────────────────────
function DashboardTab({ lBase, jBase }) {
  const [rows, setRows]         = useState([]);
  const [queueStatus, setQueue] = useState(null);
  const [logs, setLogs]         = useState([]);
  const [tpsHistory, setTps]    = useState([]);
  const [latHistory, setLat]    = useState([]);
  const logsRef                 = useRef(null);
  const prevRowsRef             = useRef({});

  const addLog = useCallback((msg, level = 'info') => {
    const ts = new Date().toLocaleTimeString();
    setLogs(l => [...l.slice(-299), { ts, msg, level }]);
  }, []);

  useEffect(() => {
    let alive = true;
    async function tick() {
      if (!alive) return;
      try {
        const data = await apiFetch(`${lBase}/leaderboard?limit=20`);
        if (!Array.isArray(data)) return;
        setRows(data);
        data.forEach(r => {
          const prev = prevRowsRef.current[r.contestant_id];
          const score = Number(r.score || 0).toFixed(1);
          if (prev !== score) {
            addLog(`Score update: ${r.contestant_id} → ${score} (p99=${r.p99_us}µs, correct=${(Number(r.correct_ratio||0)*100).toFixed(1)}%)`, 'score');
            prevRowsRef.current[r.contestant_id] = score;
          }
          const t = Date.now();
          setTps(h => [...h.slice(-119), { t, tps: Number(r.sustained_tps || 0), contestant: r.contestant_id }]);
          setLat(h => [...h.slice(-119), { t, p50: Number(r.p50_us || 0), p99: Number(r.p99_us || 0), contestant: r.contestant_id }]);
        });
      } catch (e) { addLog(`Leaderboard error: ${e.message}`, 'error'); }
      try {
        const q = await apiFetch(`${jBase}/admin/queue`);
        setQueue(q);
      } catch { /* judge may not be running */ }
    }
    tick();
    const id = setInterval(tick, 1500);
    return () => { alive = false; clearInterval(id); };
  }, [lBase, jBase, addLog]);

  useEffect(() => {
    const es = new EventSource(`${lBase}/stream`);
    es.addEventListener('update', e => addLog(`SSE: leaderboard updated (${e.data})`, 'sse'));
    es.addEventListener('error', () => addLog('SSE disconnected — reconnecting…', 'warn'));
    return () => es.close();
  }, [lBase, addLog]);

  useEffect(() => {
    if (logsRef.current) logsRef.current.scrollTop = logsRef.current.scrollHeight;
  }, [logs]);

  const topContestants = [...new Set(rows.map(r => r.contestant_id))].slice(0, 5);

  return (
    <section>
      {/* Stat KPIs */}
      {rows.length > 0 && (
        <div className="grid-4 mb-16" style={{ marginBottom: 20 }}>
          <div className="card">
            <div className="stat-label">Top Score</div>
            <div className="stat-value">{Number(rows[0]?.score||0).toFixed(1)}</div>
            <div className="stat-sub">{rows[0]?.contestant_id}</div>
          </div>
          <div className="card">
            <div className="stat-label">Live Contestants</div>
            <div className="stat-value">{rows.length}</div>
            <div className="stat-sub">on leaderboard</div>
          </div>
          <div className="card">
            <div className="stat-label">Top p99 Latency</div>
            <div className="stat-value" style={{ fontSize: '1.25rem' }}>{rows[0]?.p99_us || '—'}<span style={{ fontSize: '0.75rem', fontWeight: 400 }}>µs</span></div>
            <div className="stat-sub">99th percentile</div>
          </div>
          <div className="card">
            <div className="stat-label">Top TPS</div>
            <div className="stat-value">{Number(rows[0]?.sustained_tps||0).toFixed(0)}</div>
            <div className="stat-sub">sustained orders/s</div>
          </div>
        </div>
      )}

      {/* Sparkline charts */}
      <div className="grid-2" style={{ marginBottom: 16 }}>
        <div className="card">
          <div className="card-header">
            <div className="card-icon card-icon-cyan">📈</div>
            <span className="card-title">Sustained TPS</span>
          </div>
          <Sparkline data={tpsHistory} contestants={topContestants} colors={PALETTE} valueKey="tps" />
        </div>
        <div className="card">
          <div className="card-header">
            <div className="card-icon card-icon-violet">⏱</div>
            <span className="card-title">p99 Latency (µs)</span>
          </div>
          <Sparkline data={latHistory} contestants={topContestants} colors={PALETTE} valueKey="p99" />
        </div>
      </div>

      {/* Sandbox + Score Breakdown */}
      <div className="grid-2" style={{ marginBottom: 16 }}>
        <div className="card">
          <div className="card-header">
            <div className="card-icon card-icon-emerald">🖥</div>
            <span className="card-title">Sandbox Status</span>
          </div>
          {queueStatus ? (
            <>
              <div className="queue-row"><span className="queue-key">Queue pending</span><span className="queue-val">{queueStatus.pending}</span></div>
              <div className="queue-row"><span className="queue-key">Running</span><span className="queue-val">{queueStatus.running}</span></div>
              <div style={{ marginTop: 14 }}>
                {rows.slice(0, 5).map((r, i) => (
                  <div key={r.contestant_id} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                    <span style={{ width: 8, height: 8, borderRadius: '50%', background: PALETTE[i % PALETTE.length], display: 'inline-block', boxShadow: `0 0 6px ${PALETTE[i%PALETTE.length]}66` }} />
                    <span style={{ fontSize: '0.8125rem', flex: 1, color: 'var(--text-secondary)' }}>{r.contestant_id}</span>
                    <span style={{ fontSize: '0.875rem', fontWeight: 700, color: 'var(--accent-cyan)' }}>{Number(r.score||0).toFixed(1)}</span>
                  </div>
                ))}
              </div>
            </>
          ) : (
            <p className="text-muted text-sm">Judge API not reachable</p>
          )}
        </div>
        <div className="card">
          <div className="card-header">
            <div className="card-icon card-icon-amber">🎯</div>
            <span className="card-title">Score Breakdown (leader)</span>
          </div>
          {rows[0] ? <ScoreBreakdown row={rows[0]} /> : <p className="text-muted text-sm">No data yet. Run the bot fleet to begin.</p>}
        </div>
      </div>

      {/* Diagnostic log */}
      <div className="card">
        <div className="card-header">
          <div className="card-icon card-icon-violet">💬</div>
          <span className="card-title">Diagnostic Log</span>
          <span style={{ marginLeft: 'auto', fontSize: '0.6875rem', color: 'var(--text-muted)' }}>{logs.length} events</span>
        </div>
        <div className="log-terminal" ref={logsRef}>
          {logs.length === 0
            ? <span className="log-empty">Waiting for events…</span>
            : logs.map((l, i) => (
                <div key={i} className="log-entry">
                  <span className="log-timestamp">[{l.ts}]</span>
                  <span style={{ color: l.level === 'error' ? 'var(--accent-rose)' : l.level === 'warn' ? 'var(--accent-amber)' : l.level === 'score' ? 'var(--accent-emerald)' : l.level === 'sse' ? 'var(--accent-violet)' : undefined }}>{l.msg}</span>
                </div>
              ))}
        </div>
      </div>
    </section>
  );
}

// ── Sparkline SVG component ───────────────────────────────────────────────────
function Sparkline({ data, contestants, colors, valueKey }) {
  const W = 480, H = 90, PAD = 6;
  if (data.length < 2) return (
    <div className="sparkline-empty">Collecting data…</div>
  );

  const allVals = data.map(d => d[valueKey]);
  const maxV = Math.max(...allVals, 1);
  const minV = Math.min(...allVals, 0);

  const lines = contestants.map((cid, ci) => {
    const pts = data.filter(d => d.contestant === cid);
    if (pts.length < 2) return null;
    const vals = pts.map(d => d[valueKey]);
    const lo = Math.min(...vals), hi = Math.max(...vals, lo + 1);
    const xs = pts.map((_, i) => PAD + (i / (pts.length - 1)) * (W - 2 * PAD));
    const ys = vals.map(v => H - PAD - ((v - lo) / (hi - lo)) * (H - 2 * PAD));
    const path = xs.map((x, i) => `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${ys[i].toFixed(1)}`).join(' ');
    const col = colors[ci % colors.length];
    return (
      <g key={cid}>
        {/* Glow path */}
        <path d={path} fill="none" stroke={col} strokeWidth="3" opacity="0.2" strokeLinecap="round" strokeLinejoin="round" />
        {/* Main path */}
        <path d={path} fill="none" stroke={col} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
      </g>
    );
  }).filter(Boolean);

  return (
    <div className="sparkline-wrap">
      <svg className="sparkline-svg" viewBox={`0 0 ${W} ${H}`} aria-label="Sparkline chart">
        {/* Background grid */}
        {[0.25, 0.5, 0.75].map(f => (
          <line key={f} x1={PAD} y1={H * f} x2={W - PAD} y2={H * f}
            stroke="rgba(99,120,200,0.1)" strokeWidth="1" strokeDasharray="4,4" />
        ))}
        {lines}
      </svg>
      <div className="sparkline-legend">
        {contestants.map((cid, i) => (
          <span key={cid} className="sparkline-legend-item">
            <span className="sparkline-dot" style={{ background: colors[i % colors.length] }} />
            {cid}
          </span>
        ))}
        <span className="sparkline-max">max: {maxV.toFixed(0)}</span>
      </div>
    </div>
  );
}

// ── Score breakdown ───────────────────────────────────────────────────────────
function ScoreBreakdown({ row }) {
  const sL     = Number(row.score_latency     || 0);
  const sT     = Number(row.score_throughput  || 0);
  const sC     = Number(row.score_correctness || 0);
  const total  = Number(row.score || 0);
  const bar = (v, color) => (
    <div className="progress-bar-track">
      <div className="progress-bar-fill" style={{ width: `${Math.min(100, v).toFixed(1)}%`, background: color }} />
    </div>
  );
  return (
    <div className="score-breakdown">
      {[
        { label: 'S_L  latency (40%)',     v: sL, color: 'linear-gradient(90deg,#38bdf8,#818cf8)' },
        { label: 'S_T  throughput (30%)',  v: sT, color: 'linear-gradient(90deg,#34d399,#38bdf8)' },
        { label: 'S_C  correctness (30%)', v: sC, color: 'linear-gradient(90deg,#fbbf24,#f97316)' },
      ].map(({ label, v, color }) => (
        <div className="score-row" key={label}>
          <span className="score-row-label text-xs text-muted">{label}</span>
          {bar(v, color)}
          <span className="score-row-value">{v.toFixed(1)}</span>
        </div>
      ))}
      <div className="score-row score-total-row">
        <span className="score-row-label score-total-label">S_Total</span>
        {bar(total, 'var(--gradient-score)')}
        <span className="score-row-value score-total-value">{total.toFixed(1)}</span>
      </div>
    </div>
  );
}

// ── Leaderboard ───────────────────────────────────────────────────────────────
function LeaderboardTab({ lBase }) {
  const [rows, setRows]       = useState([]);
  const [err, setErr]         = useState('');
  const [loading, setLoading] = useState(false);
  const timerRef              = useRef(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      setErr('');
      const data = await apiFetch(`${lBase}/leaderboard?limit=100`);
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

  const COLS = ['Rank','Contestant','Orders','TPS','Correct%','p50µs','p90µs','p99µs','S_L','S_T','S_C','Score'];

  return (
    <section>
      <div className="section-header">
        <h1 className="section-title">🏆 Live Leaderboard</h1>
        <button id="btn-refresh-leaderboard" onClick={refresh} className="btn btn-ghost" disabled={loading}>
          {loading ? <><Spinner /> Refreshing…</> : '↻ Refresh'}
        </button>
        <span style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>Auto-updates via SSE</span>
      </div>
      {err && <ErrorBox msg={err} />}
      <div className="data-table-wrap">
        <table className="data-table" aria-label="Leaderboard">
          <thead>
            <tr>{COLS.map(h => <th key={h}>{h}</th>)}</tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={r.contestant_id || i}>
                <td className={`rank-col${i === 0 ? ' rank-1' : i === 1 ? ' rank-2' : i === 2 ? ' rank-3' : ''}`}>{i+1}</td>
                <td>
                  <div className="contestant-col">
                    <div className="contestant-avatar" style={{ background: `linear-gradient(135deg, ${PALETTE[i % PALETTE.length]}, ${PALETTE[(i+2) % PALETTE.length]})` }}>
                      {(r.contestant_id || '?').slice(0, 2).toUpperCase()}
                    </div>
                    <span className="text-mono text-sm">{r.contestant_id}</span>
                  </div>
                </td>
                <td>{r.count?.toLocaleString()}</td>
                <td className="text-bold">{Number(r.sustained_tps || 0).toFixed(0)}</td>
                <td style={{ color: Number(r.correct_ratio) >= 0.99 ? 'var(--accent-emerald)' : Number(r.correct_ratio) >= 0.95 ? 'var(--accent-amber)' : 'var(--accent-rose)' }}>
                  {(Number(r.correct_ratio || 0) * 100).toFixed(2)}%
                </td>
                <td className="text-mono text-sm">{r.p50_us}</td>
                <td className="text-mono text-sm">{r.p90_us}</td>
                <td className="text-mono text-sm">{r.p99_us}</td>
                <td>{Number(r.score_latency     || 0).toFixed(1)}</td>
                <td>{Number(r.score_throughput  || 0).toFixed(1)}</td>
                <td>{Number(r.score_correctness || 0).toFixed(1)}</td>
                <td className="score-col">{Number(r.score || 0).toFixed(1)}</td>
              </tr>
            ))}
            {rows.length === 0 && !loading && (
              <tr>
                <td colSpan={12} style={{ textAlign: 'center', padding: '32px', color: 'var(--text-muted)' }}>
                  No contestants yet. Submit an engine to get started.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  );
}

// ── Submit ────────────────────────────────────────────────────────────────────
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
    <section style={{ maxWidth: 600 }}>
      <h1 className="section-title" style={{ marginBottom: 8 }}>🚀 Submit Your Engine</h1>
      <p className="text-sm text-muted" style={{ marginBottom: 24, lineHeight: 1.6 }}>
        Your Docker image must implement <code className="text-cyan">TradingGateway</code> gRPC on port <code className="text-cyan">50051</code>. The judge validates the engine, then runs a bot fleet (60% MARKET, 30% LIMIT, 10% CANCEL probes).
      </p>

      {/* Info panel */}
      <div className="alert alert-info" style={{ marginBottom: 24 }}>
        <span>ℹ</span>
        <div>
          <strong>Scoring:</strong> S_Total = 0.40·S_L + 0.30·S_T + 0.30·S_C — correctness (S_C) uses a 4th-power penalty for errors.
        </div>
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <form onSubmit={submit} style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div className="input-group">
            <label className="input-label" htmlFor="submit-contestant-id">Contestant ID</label>
            <input id="submit-contestant-id" className="input-field" value={contestantId} onChange={e => setContestantId(e.target.value)} placeholder="team-alpha" required />
          </div>
          <div className="input-group">
            <label className="input-label" htmlFor="submit-image-tag">Docker Image Tag</label>
            <input id="submit-image-tag" className="input-field" value={imageTag} onChange={e => setImageTag(e.target.value)} placeholder="registry/engine:v1" required />
          </div>
          <button id="btn-submit-engine" type="submit" disabled={loading} className="btn btn-primary" style={{ alignSelf: 'flex-start' }}>
            {loading ? <><Spinner /> Submitting…</> : '🚀 Submit'}
          </button>
        </form>
      </div>

      {err && <ErrorBox msg={err} />}
      {result && (
        <SuccessBox>
          <strong>Accepted!</strong> Submission queued for judging.
          <br />
          <span className="text-muted text-sm">Switch to <strong>My Runs</strong> to track progress.</span>
          <pre className="text-mono" style={{ fontSize: '0.6875rem', marginTop: 8, color: 'var(--text-muted)', whiteSpace: 'pre-wrap' }}>{JSON.stringify(result, null, 2)}</pre>
        </SuccessBox>
      )}
    </section>
  );
}

// ── My Runs ───────────────────────────────────────────────────────────────────
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
    clearInterval(intervalRef.current);
    if (hasRunning) { intervalRef.current = setInterval(() => fetchRuns(contestantId), 3000); }
    return () => clearInterval(intervalRef.current);
  }, [runs, contestantId, fetchRuns]);

  async function handleSubmit(e) {
    e.preventDefault(); setLoading(true); await fetchRuns(contestantId); setLoading(false);
  }

  const COLS = ['Run ID','Status','Started','Duration','Orders','Correct%','p50µs','p99µs','Score','Error'];

  return (
    <section>
      <h1 className="section-title" style={{ marginBottom: 20 }}>📊 My Runs</h1>
      <div className="card" style={{ marginBottom: 20 }}>
        <form onSubmit={handleSubmit} className="form-row">
          <div className="input-group">
            <label className="input-label" htmlFor="runs-contestant-id">Contestant ID</label>
            <input id="runs-contestant-id" className="input-field" value={contestantId} onChange={e => setContestantId(e.target.value)} placeholder="team-alpha" required style={{ minWidth: 220 }} />
          </div>
          <button id="btn-load-runs" type="submit" disabled={loading} className="btn btn-primary">
            {loading ? <><Spinner /> Loading…</> : 'Load Runs'}
          </button>
        </form>
      </div>
      {err && <ErrorBox msg={err} />}
      {runs !== null && (runs.length === 0
        ? <div className="card" style={{ textAlign: 'center', padding: '32px', color: 'var(--text-muted)' }}>No runs found for this contestant.</div>
        : <div className="data-table-wrap">
            <table className="data-table" aria-label="My Runs">
              <thead><tr>{COLS.map(h => <th key={h}>{h}</th>)}</tr></thead>
              <tbody>
                {runs.map((r) => {
                  const dur = r.finished_at ? ((new Date(r.finished_at) - new Date(r.started_at)) / 1000).toFixed(1) + 's' : '—';
                  return (
                    <tr key={r.run_id}>
                      <td className="text-mono text-xs" style={{ color: 'var(--text-muted)' }}>{r.run_id?.slice(0, 28)}…</td>
                      <td>{statusBadge(r.status)}</td>
                      <td className="text-mono text-xs">{r.started_at ? new Date(r.started_at).toLocaleTimeString() : '—'}</td>
                      <td>{dur}</td>
                      <td className="text-bold">{r.total_orders?.toLocaleString() || '—'}</td>
                      <td style={{ color: r.correct_ratio >= 0.99 ? 'var(--accent-emerald)' : r.correct_ratio >= 0.95 ? 'var(--accent-amber)' : r.correct_ratio != null ? 'var(--accent-rose)' : undefined }}>
                        {r.correct_ratio != null ? (r.correct_ratio * 100).toFixed(2) + '%' : '—'}
                      </td>
                      <td className="text-mono text-xs">{r.p50_us || '—'}</td>
                      <td className="text-mono text-xs">{r.p99_us || '—'}</td>
                      <td className="score-col">{r.score != null ? Number(r.score).toFixed(1) : '—'}</td>
                      <td className="text-xs" style={{ color: 'var(--accent-rose)', maxWidth: 200, wordBreak: 'break-all' }}>{r.error || ''}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
      )}
    </section>
  );
}

// ── History ───────────────────────────────────────────────────────────────────
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

  const COLS = ['Window','Orders','Correct%','p50µs','p90µs','p99µs','Avg µs','Min µs','Max µs'];

  return (
    <section>
      <h1 className="section-title" style={{ marginBottom: 20 }}>📈 Performance History</h1>
      <p className="text-sm text-muted" style={{ marginBottom: 20 }}>Per-minute aggregated stats from TimescaleDB.</p>
      <div className="card" style={{ marginBottom: 20 }}>
        <form onSubmit={fetchHistory} className="form-row">
          <div className="input-group">
            <label className="input-label" htmlFor="history-contestant-id">Contestant ID</label>
            <input id="history-contestant-id" className="input-field" value={contestantId} onChange={e => setContestantId(e.target.value)} placeholder="team-alpha" required style={{ minWidth: 220 }} />
          </div>
          <div className="input-group" style={{ minWidth: 140 }}>
            <label className="input-label" htmlFor="history-window">Window</label>
            <select id="history-window" className="select-field" value={hours} onChange={e => setHours(e.target.value)}>
              <option value="1">Last 1 hour</option>
              <option value="6">Last 6 hours</option>
              <option value="24">Last 24 hours</option>
              <option value="48">Last 48 hours</option>
            </select>
          </div>
          <button id="btn-load-history" type="submit" disabled={loading} className="btn btn-primary">
            {loading ? <><Spinner /> Loading…</> : 'Load'}
          </button>
        </form>
      </div>
      {err && <ErrorBox msg={err} />}
      {data !== null && (data.length === 0
        ? <div className="card" style={{ textAlign: 'center', padding: '32px', color: 'var(--text-muted)' }}>No history found for this contestant and time window.</div>
        : <div className="data-table-wrap">
            <table className="data-table" aria-label="Performance history">
              <thead><tr>{COLS.map(h => <th key={h}>{h}</th>)}</tr></thead>
              <tbody>
                {data.map((row, i) => (
                  <tr key={i}>
                    <td className="text-mono text-xs">{row.window_start ? new Date(row.window_start).toLocaleTimeString() : '—'}</td>
                    <td className="text-bold">{row.count?.toLocaleString()}</td>
                    <td style={{ color: row.correct_ratio >= 0.99 ? 'var(--accent-emerald)' : row.correct_ratio >= 0.95 ? 'var(--accent-amber)' : 'var(--accent-rose)' }}>
                      {row.correct_ratio != null ? (row.correct_ratio * 100).toFixed(2) + '%' : '—'}
                    </td>
                    <td className="text-mono text-xs">{row.p50_us}</td>
                    <td className="text-mono text-xs">{row.p90_us}</td>
                    <td className="text-mono text-xs text-bold">{row.p99_us}</td>
                    <td className="text-mono text-xs">{row.avg_us}</td>
                    <td className="text-mono text-xs">{row.min_us}</td>
                    <td className="text-mono text-xs">{row.max_us}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
      )}
    </section>
  );
}

// ── Contestants ───────────────────────────────────────────────────────────────
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
      {/* Registration form */}
      <h1 className="section-title" style={{ marginBottom: 20 }}>👥 Register Contestant</h1>
      <div className="card" style={{ marginBottom: 20, maxWidth: 600 }}>
        <form onSubmit={register} style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div className="form-row">
            <div className="input-group">
              <label className="input-label" htmlFor="reg-id">ID (slug)</label>
              <input id="reg-id" className="input-field" value={regId} onChange={e => setRegId(e.target.value)} placeholder="team-alpha" required />
            </div>
            <div className="input-group">
              <label className="input-label" htmlFor="reg-name">Display Name</label>
              <input id="reg-name" className="input-field" value={regName} onChange={e => setRegName(e.target.value)} placeholder="Team Alpha" required />
            </div>
          </div>
          <button id="btn-register-contestant" type="submit" disabled={regLoading} className="btn btn-primary" style={{ alignSelf: 'flex-start' }}>
            {regLoading ? <><Spinner /> Registering…</> : '+ Register'}
          </button>
        </form>
      </div>
      {regErr    && <ErrorBox msg={regErr} />}
      {regResult && <SuccessBox>Registered: <strong>{regResult.display_name}</strong> <span className="text-mono text-xs text-muted">({regResult.id})</span></SuccessBox>}

      {/* Contestant list */}
      <div className="section-header">
        <h2 className="section-title" style={{ fontSize: '1rem' }}>All Contestants</h2>
        <button id="btn-refresh-contestants" onClick={loadContestants} className="btn btn-ghost" disabled={listLoading}>
          {listLoading ? <><Spinner /> Loading…</> : '↻ Refresh'}
        </button>
      </div>
      {err && <ErrorBox msg={err} />}
      {contestants !== null && (contestants.length === 0
        ? <div className="card" style={{ textAlign: 'center', padding: '32px', color: 'var(--text-muted)' }}>No contestants registered yet.</div>
        : <div className="data-table-wrap">
            <table className="data-table" aria-label="Contestants list">
              <thead><tr>{['ID', 'Display Name', 'Registered At'].map(h => <th key={h}>{h}</th>)}</tr></thead>
              <tbody>
                {contestants.map((c, i) => (
                  <tr key={c.id}>
                    <td>
                      <div className="contestant-col">
                        <div className="contestant-avatar" style={{ background: `linear-gradient(135deg, ${PALETTE[i%PALETTE.length]}, ${PALETTE[(i+2)%PALETTE.length]})` }}>
                          {(c.id || '?').slice(0,2).toUpperCase()}
                        </div>
                        <span className="text-mono text-sm">{c.id}</span>
                      </div>
                    </td>
                    <td className="text-bold">{c.display_name}</td>
                    <td className="text-mono text-xs text-muted">{c.registered_at ? new Date(c.registered_at).toLocaleString() : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
      )}
    </section>
  );
}
