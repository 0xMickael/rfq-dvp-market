import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import {
  createWriteStream,
  existsSync,
  mkdirSync,
  writeFileSync,
  readFileSync,
} from "node:fs";
import { once } from "node:events";
import { createServer } from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { Contract, HDNodeWallet, JsonRpcProvider, parseUnits } from "ethers";
import { typedTrade } from "../.test-dist/terms.js";

const root = fileURLToPath(new URL("../../", import.meta.url));
const [run, apiBinary, engineBinary] = process.argv
  .slice(2)
  .map((p) => path.resolve(p));
const rpcURL = "http://127.0.0.1:28545",
  base = "http://127.0.0.1:28080";
const database =
  "postgresql://rfq@127.0.0.1:55434/postgres?sslmode=disable&connect_timeout=3";
const deployment = path.join(run, "deployment.json");
let processes = [],
  apps = [],
  iteration = 0,
  identity,
  c,
  held = false,
  loseReply = false,
  signalDirectory;
const checks = [];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function until(check, label, ms = 30000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    try {
      if (await check()) return;
    } catch {}
    await sleep(100);
  }
  throw Error(`Timeout: ${label}`);
}
function start(name, file, args = [], extra = {}) {
  const out = createWriteStream(path.join(run, `${iteration}-${name}.out.log`));
  const err = createWriteStream(path.join(run, `${iteration}-${name}.err.log`));
  const env = {
    ...process.env,
    DATABASE_URL: "",
    MARKET_ID: "",
    RFQ_TEST_MODE: "",
    RFQ_TEST_FAILPOINT: "",
    ...extra,
  };
  const p = spawn(file, args, {
    cwd: root,
    env,
    windowsHide: true,
    stdio: ["ignore", "pipe", "pipe"],
  });
  p.stdout.pipe(out);
  p.stderr.pipe(err);
  p.on("error", (e) => err.write(String(e)));
  processes.push(p);
  return p;
}
async function stop(p) {
  if (p.exitCode === null && p.signalCode === null) {
    const exited = once(p, "exit");
    p.kill("SIGKILL");
    await exited;
  }
}
async function stopApps() {
  for (const p of apps) await stop(p);
  apps = [];
}
async function json(url, body, timeout = 5000) {
  const r = await fetch(url, {
    method: body === undefined ? "GET" : "POST",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(timeout),
  });
  const value = await r.json();
  if (!r.ok) throw Error(`${r.status} ${JSON.stringify(value)}`);
  return value;
}
const api = (p, b) => json(base + p, b);
const sql = (q) =>
  execFileSync(
    path.join(root, ".cache/tools/postgresql-18.7/pgsql/bin/psql.exe"),
    [
      "-h",
      "127.0.0.1",
      "-p",
      "55434",
      "-U",
      "rfq",
      "-d",
      "postgres",
      "-At",
      "-v",
      "ON_ERROR_STOP=1",
      "-c",
      q,
    ],
    { encoding: "utf8", windowsHide: true },
  ).trim();
async function startApps(point = "", waitReady = true) {
  iteration++;
  signalDirectory = path.join(run, `signals-${iteration}`);
  mkdirSync(signalDirectory);
  held = false;
  loseReply = point === "lost_response";
  const env = {
    DATABASE_URL: database,
    MARKET_ID: identity,
    RFQ_TEST_MODE: "1",
    RFQ_TEST_FAILPOINT: point,
    RFQ_TEST_SIGNAL_DIR: signalDirectory,
  };
  apps.push(
    start("engine", engineBinary, [], {
      ...env,
      ENGINE_ADDR: "127.0.0.1:28081",
    }),
  );
  await until(
    async () =>
      (await json("http://127.0.0.1:28081/market")).makers.length === 3,
    "Rust durable",
  );
  apps.push(
    start(
      "api",
      apiBinary,
      [
        "-deployment",
        deployment,
        "-rpc",
        "http://127.0.0.1:28546",
        "-listen",
        "127.0.0.1:28080",
        "-engine",
        "http://127.0.0.1:28081",
      ],
      env,
    ),
  );
  if (waitReady)
    await until(
      async () => (await api("/api/readiness")).status === "ready",
      "Go reconciled",
    );
}
// For the lost-response case, the real node accepts the raw transaction, but Go
// never receives its reply. The harness kills Go while this response is withheld.
const proxy = createServer(async (req, res) => {
  try {
    let raw = "";
    for await (const chunk of req) raw += chunk;
    if (held) return;
    const upstream = await fetch(rpcURL, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: raw,
    });
    const body = await upstream.text();
    if (loseReply && JSON.parse(raw).method === "eth_sendRawTransaction") {
      assert.ok(
        JSON.parse(body).result,
        "Anvil must accept the transaction before losing the reply",
      );
      held = true;
      loseReply = false;
      writeFileSync(path.join(signalDirectory, "lost_response.ready"), "ready");
      return;
    }
    res.writeHead(upstream.status, { "Content-Type": "application/json" });
    res.end(body);
  } catch {
    res.writeHead(502);
    res.end("{}");
  }
});
await new Promise((resolve) => proxy.listen(28546, "127.0.0.1", resolve));
const provider = new JsonRpcProvider(rpcURL, 31337, { cacheTimeout: -1 });
const trader = HDNodeWallet.fromPhrase(
  "test test test test test test test test test test test junk",
  undefined,
  "m/44'/60'/0'/0/1",
).connect(provider);
try {
  start("anvil", "anvil", [
    "--host",
    "127.0.0.1",
    "--port",
    "28545",
    "--chain-id",
    "31337",
    "--hardfork",
    "cancun",
    "--silent",
  ]);
  await until(
    async () => (await provider.send("eth_chainId", [])) === "0x7a69",
    "Anvil",
  );
  const forge = start(
    "deploy",
    "forge",
    [
      "script",
      "contracts/script/DeployLocal.s.sol:DeployLocal",
      "--root",
      "contracts",
      "--rpc-url",
      rpcURL,
      "--broadcast",
      "--non-interactive",
    ],
    {
      DEPLOYMENT_FILE: deployment.replaceAll("\\", "/"),
      FOUNDRY_BROADCAST: path.join(run, "broadcast"),
      FOUNDRY_CACHE_PATH: path.join(run, "forge-cache"),
    },
  );
  await once(forge, "exit");
  assert.equal(forge.exitCode, 0, "deployment");
  c = JSON.parse(readFileSync(deployment, "utf8"));
  identity = execFileSync(
    apiBinary,
    ["-deployment", deployment, "-rpc", rpcURL, "-print-market-id"],
    { encoding: "utf8", windowsHide: true },
  ).trim();
  const erc = [
    "function balanceOf(address) view returns(uint256)",
    "function transfer(address,uint256) returns(bool)",
    "function approve(address,uint256) returns(bool)",
  ];
  const cash = new Contract(c.cashToken, erc, trader),
    security = new Contract(c.securityToken, erc, trader);
  const artifact = JSON.parse(
    readFileSync(
      path.join(root, "contracts/out/DvPSettlement.sol/DvPSettlement.json"),
    ),
  );
  const dvp = new Contract(c.settlement, artifact.abi, provider);
  async function prepare(side) {
    const rfq = await api("/api/rfqs", {
      trader_id: trader.address,
      side,
      unit: "tBILL",
      amount: "1.123456",
    });
    const q = rfq.quotes[0],
      data = typedTrade(c, rfq, q);
    const signature = await trader.signTypedData(
      data.domain,
      data.types,
      data.message,
    );
    return {
      rfq,
      q,
      data,
      payload: { quote_id: q.quote_id, trade_id: q.trade_id, signature },
    };
  }
  async function settled(p) {
    let result;
    await until(async () => {
      result = await api(`/api/settlements/${p.q.trade_id}`);
      return result.status === "SETTLED";
    }, "settlement after restart");
    return result;
  }
  for (const side of ["BUY", "SELL"])
    for (const point of [
      "after_reservation",
      "after_sign",
      "lost_response",
      "after_inclusion",
      "after_accounting",
    ]) {
      await startApps(point);
      const p = await prepare(side),
        maker = c[p.q.maker_id.toLowerCase()];
      const before = await Promise.all([
        cash.balanceOf(trader.address),
        security.balanceOf(trader.address),
        cash.balanceOf(maker),
        security.balanceOf(maker),
      ]);
      // Do not wait for a response: several failpoints intentionally suspend the request.
      const accepting = api(
        `/api/rfqs/${p.rfq.rfq_id}/accept`,
        p.payload,
      ).catch(() => null);
      await until(
        () => existsSync(path.join(signalDirectory, `${point}.ready`)),
        point,
      );
      const storedHash = sql(
        `SELECT COALESCE(transaction_hash,'') FROM settlement_jobs WHERE trade_id='${p.q.trade_id}'`,
      );
      const beforeRestart = sql(
        `SELECT count(*) FROM acceptance_intents WHERE trade_id='${p.q.trade_id}'`,
      );
      assert.equal(beforeRestart, "1");
      await stopApps();
      held = false;
      await accepting;
      await startApps();
      const result = await settled(p);
      if (storedHash)
        assert.equal(
          result.transaction_hash,
          storedHash,
          "same signed transaction after crash",
        );
      const after = await Promise.all([
        cash.balanceOf(trader.address),
        security.balanceOf(trader.address),
        cash.balanceOf(maker),
        security.balanceOf(maker),
      ]);
      const direction = side === "BUY" ? 1n : -1n,
        money = parseUnits(p.q.cash_amount, 6),
        qty = parseUnits(p.q.quantity_tbill, 6);
      assert.deepEqual(
        after.map((v, i) => v - before[i]),
        [
          -direction * money,
          direction * qty,
          direction * money,
          -direction * qty,
        ],
      );
      const events = await dvp.queryFilter(
        dvp.filters.TradeSettled(p.q.trade_id),
      );
      assert.equal(events.length, 1, "one on-chain settlement");
      const snapshot = await api("/api/market"),
        m = snapshot.makers.find((v) => v.id === p.q.maker_id);
      assert.equal(parseUnits(m.cash, 6), after[2]);
      assert.equal(parseUnits(m.tbill, 6), after[3]);
      assert.equal(m.reserved_cash, "0.000000");
      assert.equal(m.reserved_tbill, "0.000000");
      const repeat = await api(`/api/rfqs/${p.rfq.rfq_id}/accept`, p.payload);
      assert.equal(repeat.settlement.transaction_hash, result.transaction_hash);
      assert.deepEqual(await api("/api/market"), snapshot);
      checks.push({
        side,
        point,
        passed: true,
        transaction_hash: result.transaction_hash,
      });
      console.log(
        `OK ${side} ${point}: recovery with one transaction and one accounting update`,
      );
      await stopApps();
    }
  await startApps();
  // Concurrent HTTP retries share one durable authorization, reservation and job.
  const concurrent = await prepare("BUY");
  const attempts = await Promise.allSettled(
    Array.from({ length: 4 }, () =>
      api(`/api/rfqs/${concurrent.rfq.rfq_id}/accept`, concurrent.payload),
    ),
  );
  assert.ok(attempts.some((value) => value.status === "fulfilled"));
  await settled(concurrent);
  assert.equal(
    sql(
      `SELECT count(*) FROM acceptance_intents WHERE trade_id='${concurrent.q.trade_id}'`,
    ),
    "1",
  );
  assert.equal(
    sql(
      `SELECT count(*) FROM settlement_jobs WHERE trade_id='${concurrent.q.trade_id}'`,
    ),
    "1",
  );
  assert.equal(
    (await dvp.queryFilter(dvp.filters.TradeSettled(concurrent.q.trade_id)))
      .length,
    1,
  );
  checks.push({ point: "concurrent_durable_acceptance", passed: true });
  // A second writer cannot acquire either component's database lease.
  for (const [file, args, extra] of [
    [engineBinary, [], { ENGINE_ADDR: "127.0.0.1:28082" }],
    [
      apiBinary,
      [
        "-deployment",
        deployment,
        "-rpc",
        rpcURL,
        "-listen",
        "127.0.0.1:28083",
        "-engine",
        "http://127.0.0.1:28081",
      ],
      {},
    ],
  ]) {
    const duplicate = start("duplicate-" + path.basename(file), file, args, {
      DATABASE_URL: database,
      MARKET_ID: identity,
      ...extra,
    });
    await once(duplicate, "exit");
    assert.notEqual(duplicate.exitCode, 0, "second durable writer rejected");
  }
  checks.push({ point: "single_writer_leases", passed: true });
  // Database outage: neither component falls back to RAM, even after PG restarts.
  const pgctl = path.join(
    root,
    ".cache/tools/postgresql-18.7/pgsql/bin/pg_ctl.exe",
  );
  const cluster = path.join(run, "postgres");
  execFileSync(pgctl, ["-D", cluster, "-m", "fast", "-w", "stop"], {
    windowsHide: true,
    stdio: "ignore",
  });
  assert.equal((await fetch(base + "/api/readiness")).status, 503);
  assert.equal((await fetch("http://127.0.0.1:28081/market")).status, 503);
  execFileSync(
    pgctl,
    [
      "-D",
      cluster,
      "-l",
      path.join(cluster, "server.log"),
      "-o",
      "-h 127.0.0.1 -p 55434",
      "-w",
      "start",
    ],
    { windowsHide: true, stdio: "ignore" },
  );
  assert.equal(
    (await fetch(base + "/api/readiness")).status,
    503,
    "lost session lock requires process restart",
  );
  await stopApps();
  await startApps();
  checks.push({ point: "database_outage_and_restart", passed: true });
  // Reservation and authorization survive a crash past their on-chain deadline.
  {
    const point = "after_reservation";
    await stopApps();
    await startApps(point);
    const p = await prepare("BUY");
    const before = await api("/api/market");
    const accepting = api(`/api/rfqs/${p.rfq.rfq_id}/accept`, p.payload).catch(
      () => null,
    );
    await until(
      () => existsSync(path.join(signalDirectory, `${point}.ready`)),
      point + " expiry",
    );
    await stopApps();
    await accepting;
    await provider.send("evm_setNextBlockTimestamp", [
      p.rfq.settlement_deadline + 1,
    ]);
    await provider.send("evm_mine", []);
    await startApps("", false);
    await until(
      async () =>
        (await api(`/api/settlements/${p.q.trade_id}`)).status ===
        "SETTLEMENT_FAILED",
      "deadline release after crash",
    );
    assert.deepEqual(await api("/api/market"), before);
    assert.equal(
      await dvp.settled(
        p.data.message.buyer,
        p.data.message.seller,
        p.q.trade_id,
      ),
      false,
    );
    checks.push({ point: point + "_deadline", passed: true });
  }
  await until(
    async () => (await api("/api/readiness")).status === "ready",
    "ready after release",
  );
  // A foreign transfer must be detected, never silently folded into inventory.
  await (await security.transfer(c.mm1, 1n)).wait();
  await until(async () => {
    const response = await fetch(base + "/api/readiness");
    return response.status === 503;
  }, "external balance divergence");
  const snapshot = await api("/api/market");
  assert.equal(
    snapshot.makers.find((m) => m.id === "MM1").tbill,
    "1000.000000",
  );
  checks.push({ point: "external_balance_divergence", passed: true });
  // A direct internal reservation without a Go authorization remains blocked and visible.
  const orphan = await json("http://127.0.0.1:28081/rfqs", {
    trader_id: trader.address,
    side: "BUY",
    unit: "tBILL",
    amount: "1",
  });
  await json(`http://127.0.0.1:28081/rfqs/${orphan.rfq_id}/accept`, {
    trader_id: trader.address,
    quote_id: orphan.quotes[0].quote_id,
    trade_id: orphan.quotes[0].trade_id,
  });
  await stopApps();
  await startApps("", false);
  await until(async () => {
    const response = await fetch(base + "/api/readiness");
    const value = await response.json();
    return response.status === 503 && value.code === "RECOVERY_REQUIRED" && value.message.includes("orphan reservation") && value.message.includes(orphan.quotes[0].trade_id);
  }, "orphan reservation detected");
  const pending = await json("http://127.0.0.1:28081/trades/pending");
  assert.equal(pending.length, 1);
  assert.equal(pending[0].trade_id, orphan.quotes[0].trade_id);
  checks.push({ point: "orphan_reservation", passed: true });
  writeFileSync(
    path.join(run, "summary.json"),
    JSON.stringify({ passed: true, checks }, null, 2),
  );
} finally {
  await stopApps();
  for (const p of processes.reverse()) await stop(p);
  provider.destroy();
  proxy.closeAllConnections();
  await new Promise((r) => proxy.close(r));
  if (!existsSync(path.join(run, "summary.json")))
    writeFileSync(
      path.join(run, "summary.json"),
      JSON.stringify({ passed: false, checks }, null, 2),
    );
}
