// A stand-in for paseo-tui's bridge that serves a made-up set of projects,
// workspaces and agents, so the README's screenshots and GIF can be recorded
// without a real Paseo daemon (and without anyone's real projects on screen).
//
//   paseo-tui -bridge docs/demo/demo-bridge.mjs -no-update-check
//
// It speaks the same NDJSON as bridge/bridge.ts: requests on stdin, events and
// responses on stdout. One agent keeps working in a loop, and a message sent to
// any agent gets a short streamed reply.

import { createInterface } from "node:readline";

const emit = (m) => process.stdout.write(JSON.stringify(m) + "\n");
const now = Date.now();
const ago = (min) => new Date(now - min * 60_000).toISOString();
const home = "/Users/dev";

// ---------- workspaces ----------

const ws = (id, project, name, opts = {}) => ({
  id,
  projectId: "prj_" + project,
  projectDisplayName: project,
  projectRootPath: `${home}/code/${project}`,
  projectKind: "git",
  workspaceDirectory: opts.dir ?? `${home}/.paseo/worktrees/${id}`,
  name,
  title: name,
  workspaceKind: opts.kind ?? "worktree",
  status: "done",
  activityAt: ago(opts.ago ?? 5),
  createdAt: ago(90),
  statusEnteredAt: ago(opts.ago ?? 5),
  diffStat: opts.diff ? { additions: opts.diff[0], deletions: opts.diff[1] } : null,
  gitRuntime: { currentBranch: opts.branch ?? null, isDirty: Boolean(opts.diff) },
  githubRuntime: opts.pr ? { pullRequest: { number: opts.pr, state: "open", isDraft: false, isMerged: false } } : null,
});

const workspaces = [
  ws("wks_discount", "acme-shop", "Discount codes", { branch: "swift-otter", diff: [68, 4], pr: 42, ago: 1 }),
  ws("wks_checkout", "acme-shop", "Checkout validation", { branch: "brave-heron", diff: [23, 7], ago: 2 }),
  ws("wks_overview", "acme-shop", "Codebase overview", { kind: "local", branch: "main", dir: `${home}/code/acme-shop`, ago: 35 }),
  ws("wks_search", "docs-site", "Full-text search", { branch: "keen-lynx", diff: [142, 18], ago: 0 }),
  ws("wks_ratelimit", "api-gateway", "Rate limiting", { branch: "calm-walrus", diff: [12, 3], ago: 18 }),
];

// ---------- agents ----------

const usage = (inK, outK, cost, ctx) => ({
  inputTokens: 12,
  cachedInputTokens: inK * 1000,
  outputTokens: outK * 1000,
  totalCostUsd: cost,
  contextWindowMaxTokens: 200_000,
  contextWindowUsedTokens: ctx * 1000,
});

const agent = (id, wsId, title, provider, model, opts = {}) => {
  const w = workspaces.find((x) => x.id === wsId);
  return {
    id,
    provider,
    cwd: w.workspaceDirectory,
    workspaceId: wsId,
    model,
    runtimeInfo: { model },
    thinkingOptionId: opts.thinking ?? "high",
    effectiveThinkingOptionId: opts.thinking ?? "high",
    createdAt: ago(opts.created ?? 40),
    updatedAt: ago(opts.ago ?? 2),
    status: opts.status ?? "idle",
    title,
    requiresAttention: Boolean(opts.attention),
    attentionReason: opts.attention ?? null,
    currentModeId: "default",
    availableModes: [
      { id: "default", label: "Always ask" },
      { id: "acceptEdits", label: "Accept edits" },
      { id: "plan", label: "Plan" },
    ],
    pendingPermissions: opts.perms ?? [],
    activeTurn: opts.status === "running" ? { turnId: "turn_" + id, startedAt: ago(opts.runningFor ?? 1) } : null,
    lastUsage: opts.usage ?? usage(40, 3, 0.18, 34),
    project: { projectKey: w.projectId, projectName: w.projectDisplayName, workspaceName: w.name },
    labels: {},
  };
};

const question = {
  id: "perm_question",
  provider: "claude",
  name: "AskUserQuestion",
  kind: "question",
  title: "Which checks should run before payment?",
  input: {
    questions: [
      {
        question: "Which checks should run before payment?",
        header: "Checks",
        multiSelect: true,
        allowOther: true,
        options: [
          { label: "Address", description: "postcode and country match" },
          { label: "Stock", description: "every item still in stock" },
          { label: "Coupon", description: "the discount code is still valid" },
        ],
      },
      {
        question: "What should happen when a check fails?",
        header: "On failure",
        allowOther: true,
        options: [
          { label: "Block the order", description: "show the error and stop" },
          { label: "Warn only", description: "let the customer continue" },
        ],
      },
    ],
  },
};

const agents = [
  agent("a_discount", "wks_discount", "Discount codes", "claude", "claude-opus-5-5", { attention: "finished", ago: 1, usage: usage(103, 4, 0.41, 61) }),
  agent("a_review", "wks_discount", "Review the tests", "codex", "gpt-6-codex", { thinking: "medium", ago: 4 }),
  agent("a_checkout", "wks_checkout", "Checkout validation", "claude", "claude-opus-5-5", { status: "running", perms: [question], ago: 2, runningFor: 3 }),
  agent("a_overview", "wks_overview", "Codebase overview", "omp", "anthropic/claude-sonnet-5", { thinking: "medium", ago: 35 }),
  agent("a_search", "wks_search", "Full-text search", "claude", "claude-opus-5-5", { status: "running", ago: 0, runningFor: 4, usage: usage(212, 9, 0.87, 118) }),
  agent("a_ratelimit", "wks_ratelimit", "Rate limiting", "opencode", "gpt-6", { status: "error", attention: "error", ago: 18 }),
];

// ---------- transcripts ----------

let seq = 0;
const id = () => "c" + ++seq;
const user = (text) => ({ type: "user_message", text, messageId: id() });
const say = (text) => ({ type: "assistant_message", text, messageId: id() });
const think = (text) => ({ type: "reasoning", text, messageId: id() });
const shell = (command, output, status = "completed") => ({
  type: "tool_call", callId: id(), name: "Bash", status, detail: { type: "shell", command, output, exitCode: 0 },
});
const read = (filePath) => ({ type: "tool_call", callId: id(), name: "Read", status: "completed", detail: { type: "read", filePath } });
const edit = (filePath, unifiedDiff) => ({
  type: "tool_call", callId: id(), name: "Edit", status: "completed", detail: { type: "edit", filePath, unifiedDiff },
});
const todo = (items) => ({ type: "todo", items });

const histories = {
  a_discount: [
    user("Add percentage discount codes to the cart. Codes look like SAVE10 or SAVE25. Keep it inside cart.js and add tests."),
    think("The cart is a single module. I'll read it and the checkout to see how totals flow before changing anything."),
    shell("git status --short && ls src test", "src/cart.js\nsrc/checkout.js\nsrc/money.js\ntest/money.test.js"),
    read(`${home}/.paseo/worktrees/wks_discount/src/cart.js`),
    say(
      "The cart is a single `addItem` function and totals are summed in `checkout.js`. Here's the plan:\n\n" +
        "| Step | Change | File |\n|---|---|---|\n" +
        "| 1 | `applyDiscountCode(cart, code)` parses `SAVE<n>` | `src/cart.js` |\n" +
        "| 2 | `cartTotal(cart)` returns subtotal, discount and total | `src/cart.js` |\n" +
        "| 3 | Tests for valid, invalid and missing codes | `test/cart.test.js` |\n",
    ),
    todo([
      { text: "Parse discount codes", status: "completed" },
      { text: "Compute totals with the discount", status: "completed" },
      { text: "Add tests", status: "completed" },
    ]),
    edit(
      "src/cart.js",
      "@@ -10,5 +10,17 @@\n export function addItem(cart, item) {\n-  cart.items.push(item);\n" +
        "+  cart.items.push({ ...item, qty: item.qty ?? 1 });\n   return cart;\n }\n+\n+export function applyDiscountCode(cart, code) {\n" +
        "+  const m = /^SAVE(\\d{1,2})$/i.exec(code.trim());\n+  if (!m) throw new Error(`unknown code ${code}`);\n" +
        "+  cart.discount = { code: code.toUpperCase(), percent: Number(m[1]) };\n+  return cart;\n+}\n",
    ),
    shell("npm test 2>&1 | tail -4", "✔ cart › applies SAVE10\n✔ cart › rejects unknown codes\n✔ cart › totals without a code\nℹ tests 7 · pass 7 · fail 0"),
    say(
      "Discount codes work: `SAVE10` takes 10% off, unknown codes are rejected, and `npm test` passes all **7 tests**.\n\n" +
        "One thing to check: `checkout.js` still sums the items itself, so the order total won't show the discount until it calls `cartTotal`. " +
        "You asked me to stay inside `cart.js`, so I left that alone.",
    ),
  ],
  a_review: [
    user("Review the new cart tests. Anything missing?"),
    say("The tests cover valid codes, unknown codes and the no-code total. Two gaps:\n\n- a `SAVE0` or `SAVE100` edge case\n- codes with surrounding spaces\n\nBoth are one-line additions; say if you want me to add them."),
  ],
  a_checkout: [
    user("Validate the checkout form before payment."),
    think("There are several things we could check. Better ask which ones matter before writing code."),
    shell("grep -n 'submit' src/checkout.js", "41:  form.addEventListener('submit', pay);"),
    { type: "tool_call", callId: "ask1", name: "AskUserQuestion", status: "running", detail: { type: "plain_text", label: "Question" } },
  ],
  a_overview: [
    user("Give me a short overview of this codebase."),
    say("**acme-shop** is a small storefront:\n\n- `src/cart.js`: the cart and its totals\n- `src/checkout.js`: the payment form\n- `src/money.js`: rounding and currency formatting\n\nTests run with `node --test`."),
  ],
  a_search: [
    user("Add full-text search to the docs site, with a search box in the header."),
    think("Pagefind indexes the built site at build time, so search works without a server."),
    shell("npm install --save-dev pagefind", "added 1 package in 2s"),
    edit("astro.config.mjs", "@@ -4,4 +4,6 @@ export default defineConfig({\n   integrations: [mdx()],\n+  build: { format: 'directory' },\n });\n"),
    say("Pagefind is installed and wired into the build. Next: the search box in the header."),
  ],
  a_ratelimit: [
    user("Add rate limiting to the public API."),
    shell("go test ./...", "--- FAIL: TestLimiter (0.00s)\n    limiter_test.go:31: got 11 requests, want 10"),
    { type: "error", message: "The provider stopped: the session ran out of context." },
  ],
};

// the working agent's loop: a few steps, then it starts over
const searchLoop = [
  [think("The header is in src/components/Header.astro; the search box goes next to the theme switch.")],
  [{ type: "tool_call", callId: "loop-read", name: "Read", status: "running", detail: { type: "read", filePath: "src/components/Header.astro" } }],
  [{ type: "tool_call", callId: "loop-read", name: "Read", status: "completed", detail: { type: "read", filePath: "src/components/Header.astro" } }],
  [{ type: "tool_call", callId: "loop-edit", name: "Edit", status: "running", detail: { type: "edit", filePath: "src/components/Header.astro" } }],
  [{ type: "tool_call", callId: "loop-edit", name: "Edit", status: "completed", detail: { type: "edit", filePath: "src/components/Header.astro", unifiedDiff: "@@ -18,3 +18,4 @@\n   <ThemeSwitch />\n+  <Search />\n </header>\n" } }],
  [{ type: "tool_call", callId: "loop-build", name: "Bash", status: "running", detail: { type: "shell", command: "npm run build" } }],
];

// ---------- the protocol ----------

let watched = null;
let loopTimer = null;

function watch(agentId) {
  watched = agentId;
  clearInterval(loopTimer);
  emit({ type: "history", agentId, items: histories[agentId] ?? [] });
  if (agentId === "a_search") {
    let step = 0;
    loopTimer = setInterval(() => {
      if (watched !== "a_search") return;
      if (step === searchLoop.length) {
        step = 0;
        emit({ type: "history", agentId, items: histories.a_search });
        return;
      }
      for (const item of searchLoop[step++]) emit({ type: "event", agentId, event: { type: "timeline", item } });
    }, 1400);
  }
}

// a short streamed reply
function stream(agentId, text) {
  const ev = (item) => emit({ type: "event", agentId, event: { type: "timeline", item } });
  const mid = id();
  text.split(" ").forEach((w, i) => setTimeout(() => ev({ type: "assistant_message", text: (i ? " " : "") + w, messageId: mid }), 300 + i * 70));
}

function reply(agentId, text) {
  emit({ type: "event", agentId, event: { type: "timeline", item: user(text) } });
  stream(agentId, "Done. I added both edge cases to test/cart.test.js and all 9 tests pass.");
}

// an answered question: the agent continues
function answered(agentId) {
  const a = agents.find((x) => x.id === agentId);
  if (!a) return;
  a.pendingPermissions = [];
  emit({ type: "agents", agents });
  emit({ type: "event", agentId, event: { type: "timeline", item: { type: "tool_call", callId: "ask1", name: "AskUserQuestion", status: "completed", detail: { type: "plain_text", label: "Question" } } } });
  stream(agentId, "Thanks. I'll check the address, stock and coupon on submit, and block the order with a clear message when one fails.");
}

const models = {
  claude: [
    { id: "claude-opus-5-5", label: "Opus 5.5", thinkingOptions: [{ id: "low", label: "Low" }, { id: "medium", label: "Medium" }, { id: "high", label: "High" }] },
    { id: "claude-sonnet-5", label: "Sonnet 5", thinkingOptions: [{ id: "low", label: "Low" }, { id: "high", label: "High" }] },
  ],
  codex: [{ id: "gpt-6-codex", label: "GPT-6 Codex", thinkingOptions: [{ id: "medium", label: "Medium" }, { id: "high", label: "High" }] }],
  omp: [{ id: "anthropic/claude-sonnet-5", label: "Claude Sonnet 5", thinkingOptions: [{ id: "medium", label: "Medium" }] }],
  opencode: [{ id: "gpt-6", label: "GPT-6", thinkingOptions: [] }],
};

const commands = [
  { name: "review", description: "Review the changes in this workspace", argumentHint: "[focus]", kind: "command" },
  { name: "compact", description: "Compact the conversation", argumentHint: "", kind: "command" },
  { name: "skill:tdd", description: "Test-driven development: red, green, refactor", argumentHint: "", kind: "skill" },
  { name: "skill:frontend-design", description: "Polished, production-grade interfaces", argumentHint: "", kind: "skill" },
];

function handle(req) {
  switch (req.op) {
    case "watch":
      watch(req.agentId);
      return null;
    case "unwatch":
      watched = null;
      return null;
    case "send":
      reply(req.agentId, req.text);
      return { accepted: true };
    case "permit":
      answered(req.agentId);
      return null;
    case "models":
      return { models: models[req.provider] ?? [] };
    case "providers":
      return [
        { provider: "claude", label: "Claude", status: "available", enabled: "Enabled", defaultMode: "default" },
        { provider: "codex", label: "Codex", status: "available", enabled: "Enabled", defaultMode: "default" },
        { provider: "omp", label: "Oh My Pi", status: "available", enabled: "Enabled", defaultMode: "full" },
      ];
    case "branches":
      return ["main", "release/2.4", "swift-otter", "brave-heron"];
    case "commands":
      return commands;
    case "subagents":
      return {
        subagents:
          req.agentId === "a_search"
            ? [
                { id: "s1", parentAgentId: "a_search", provider: "claude", title: "Explore", description: "Find where the header is built", status: "completed", createdAt: ago(3), updatedAt: ago(2) },
                { id: "s2", parentAgentId: "a_search", provider: "claude", title: "Explore", description: "Check Pagefind's Astro integration", status: "running", createdAt: ago(1), updatedAt: ago(0) },
              ]
            : [],
      };
    case "history":
      return agents.map((a) => ({ ...a }));
    default:
      return null;
  }
}

emit({ type: "hello", host: "home ~/.paseo", local: true });
setTimeout(() => {
  emit({ type: "connected", host: "home ~/.paseo" });
  emit({ type: "agents", agents });
  emit({ type: "workspaces", workspaces, emptyProjects: [] });
}, 150);

createInterface({ input: process.stdin }).on("line", (line) => {
  let req;
  try {
    req = JSON.parse(line);
  } catch {
    return;
  }
  emit({ type: "res", id: req.id, ok: true, data: handle(req) ?? null });
});
