import { Html, Head, Main, NextScript } from 'next/document';

export default function Document() {
  return (
    <Html lang="en">
      <Head>
        <meta charSet="utf-8" />
        <meta name="description" content="IICPC 2026 — Distributed Trading Engine Benchmarking Platform. Live leaderboard, submission portal, and real-time performance analytics." />
        <meta name="keywords" content="IICPC, trading engine, benchmark, leaderboard, competitive programming" />
        <meta name="theme-color" content="#080c18" />
        <meta property="og:type" content="website" />
        <meta property="og:title" content="IICPC 2026 — Trading Engine Challenge" />
        <meta property="og:description" content="Live leaderboard for the IICPC Distributed Benchmarking Platform" />
        {/* Preconnect for Google Fonts */}
        <link rel="preconnect" href="https://fonts.googleapis.com" />
        <link rel="preconnect" href="https://fonts.gstatic.com" crossOrigin="anonymous" />
        {/* Favicon */}
        <link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'><rect width='32' height='32' rx='8' fill='%23111827'/><text y='22' x='5' font-size='18'>⚡</text></svg>" />
      </Head>
      <body>
        <Main />
        <NextScript />
      </body>
    </Html>
  );
}
