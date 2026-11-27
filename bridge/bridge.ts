// paseo-tui bridge: the TUI's only path to Paseo. It keeps one live connection
// to a daemon through Paseo's own client library (so handshake, auth, relay and
// version handling match the CLI exactly), reuses the CLI's command code for
// creating agents and starting the daemon, and speaks NDJSON over stdio.
//
//   stdin  <- {"id":1,"op":"watch","agentId":"…"}
//   stdout -> {"type":"agents",…} | {"type":"event",…} | {"type":"res","id":1,…}

// stdout carries protocol frames only; route any library chatter to stderr.
const toStderr = (...args: unknown[]) => process.stderr.write(args.map(String).join(" ") + "\n");
console.log = toStderr;
console.info = toStderr;
console.warn = toStderr;
console.debug = toStderr;

import { createInterface } from "node:readline";
import { readFile } from "node:fs/promises";
import { extname } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";
import { connectToDaemon } from "paseo-cli/utils/client.js";
import { selectDaemonTarget, describeDaemonTarget } from "paseo-cli/utils/daemon-target.js";
import { fetchProjectedTimelineItems } from "paseo-cli/utils/timeline.js";
import { runRunCommand } from "paseo-cli/commands/agent/run.js";
import { runStart } from "paseo-cli/commands/daemon/start.js";

type Json = Record<string, unknown>;
type Client = Awaited<ReturnType<typeof connectToDaemon>>;

function emit(msg: Json): void {
  process.stdout.write(JSON.stringify(msg) + "\n");
}

function errMessage(err: unknown): string {
  if (err && typeof err === "object" && "message" in err) return String((err as Json).message);
  return String(err);
}

function errCode(err: unknown): string {
  return err && typeof err === "object" && "code" in err ? String((err as Json).code) : "";
}

function parseArgs(argv: string[]): { host?: string; home?: string } {
  const out: { host?: string; home?: string } = {};
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === "--host") out.host = argv[++i];
    else if (argv[i] === "--home") out.home = argv[++i];
  }
  return out;
}

// When launched from a Paseo terminal these would make every new agent a child of
// that agent (or force its workspace); the TUI always says explicitly what it wants.
delete process.env.PASEO_AGENT_ID;
delete process.env.PASEO_WORKSPACE_ID;

const target = selectDaemonTarget(parseArgs(process.argv.slice(2)));
let client: Client | null = null;

// ---------- agent directory (live) ----------
const agents = new Map<string, Json>();
let includeArchived = false;
let flushTimer: NodeJS.Timeout | null = null;
let releaseAgents: (() => Promise<void>) | null = null;

function scheduleFlush(): void {
  if (flushTimer) return;
  flushTimer = setTimeout(() => {
    flushTimer = null;
    emit({ type: "agents", agents: [...agents.values()] });
  }, 80);
}

async function observeAgents(c: Client): Promise<void> {
  if (releaseAgents) await releaseAgents().catch(() => {});
  agents.clear();
  const sub = c.observeAgents({
    filter: { includeArchived },
    page: { limit: 200 },
    sort: [{ key: "updated_at", direction: "desc" }],
  } as never);
  sub.subscribe({
    snapshot(snap) {
      agents.clear();
      for (const e of ((snap as unknown as Json).entries as Json[]) ?? []) {
        const agent = e.agent as Json;
        agents.set(agent.id as string, { ...agent, project: e.project ?? null });
      }
      scheduleFlush();
    },
    update(message) {
      const m = message as unknown as { type: string; payload: Json };
      if (m.type !== "agent_update") return;
      const p = m.payload;
      if (p.kind === "upsert") {
        const agent = p.agent as Json;
        if (!includeArchived && agent.archivedAt) agents.delete(agent.id as string);
        else agents.set(agent.id as string, { ...agent, project: p.project ?? null });
      } else if (p.kind === "remove") {
        agents.delete(p.agentId as string);
      }
      scheduleFlush();
    },
    error(err) {
      emit({ type: "warn", message: "agent directory: " + errMessage(err) });
    },
  });
  releaseAgents = () => sub.release();
  await sub.ready;
}

// ---------- workspaces (live) ----------
const workspaces = new Map<string, Json>();
let emptyProjects: Json[] = [];
let wsTimer: NodeJS.Timeout | null = null;
let releaseWorkspaces: (() => Promise<void>) | null = null;

function scheduleWsFlush(): void {
  if (wsTimer) return;
  wsTimer = setTimeout(() => {
    wsTimer = null;
    emit({ type: "workspaces", workspaces: [...workspaces.values()], emptyProjects });
  }, 80);
}

async function observeWorkspaces(c: Client): Promise<void> {
  if (releaseWorkspaces) await releaseWorkspaces().catch(() => {});
  workspaces.clear();
  const sub = c.observeWorkspaces({
    page: { limit: 200 },
    sort: [{ key: "activity_at", direction: "desc" }],
  } as never);
  sub.subscribe({
    snapshot(snap) {
      const s = snap as unknown as Json;
      workspaces.clear();
      for (const w of (s.entries as Json[]) ?? []) workspaces.set(w.id as string, w);
      emptyProjects = (s.emptyProjects as Json[]) ?? [];
      scheduleWsFlush();
    },
    update(message) {
      const m = message as unknown as { type: string; payload: Json };
      if (m.type !== "workspace_update") return;
      const p = m.payload;
      if (p.kind === "upsert") {
        const w = p.workspace as Json;
        workspaces.set(w.id as string, w);
      } else if (p.kind === "remove") {
        workspaces.delete(p.id as string);
      }
      scheduleWsFlush();
    },
    error(err) {
      emit({ type: "warn", message: "workspaces: " + errMessage(err) });
    },
  });
  releaseWorkspaces = () => sub.release();
  await sub.ready;
}

// ---------- provider subagents (live) ----------
async function observeSubagents(c: Client): Promise<void> {
  const sub = c.observeEvents(["agent.provider_subagents.update", "project.update"] as never);
  sub.subscribe({
    snapshot() {},
    update(message) {
      const m = message as unknown as { type: string; payload: Json };
      if (m.type === "agent.provider_subagents.update") emit({ type: "subagent_update", payload: m.payload });
      if (m.type === "project.update") emit({ type: "project_update", payload: m.payload });
    },
    error(err) {
      emit({ type: "warn", message: "subagents: " + errMessage(err) });
    },
  });
  await sub.ready;
}

// ---------- timeline of the watched agent ----------
let watchGen = 0;
let watched: { agentId: string; tail: number } | null = null;
let unwatch: (() => void) | null = null;

async function watch(c: Client, agentId: string, tail: number): Promise<void> {
  const gen = ++watchGen;
  watched = { agentId, tail };
  unwatch?.();
  unwatch = null;
  const history = await fetchProjectedTimelineItems({ client: c, agentId, timeoutMs: 8000 }).catch(
    (err) => {
      emit({ type: "warn", message: "history: " + errMessage(err) });
      return [] as unknown[];
    },
  );
  if (gen !== watchGen) return;
  emit({ type: "history", agentId, items: tail > 0 ? history.slice(-tail) : history });
  unwatch = c.subscribeAgentTimeline(agentId, (message) => {
    if (gen !== watchGen) return;
    const m = message as unknown as { type: string; payload: Json };
    switch (m.type) {
      case "agent.timeline.replacement":
      case "agent.timeline.subscription_restored":
        // events may have been missed: rebuild the view from history
        void watch(c, agentId, tail);
        return;
      case "agent.timeline.error":
        emit({ type: "warn", message: "timeline: " + String(m.payload?.error) });
        return;
    }
    emit({ type: "event", agentId, event: m.payload.event, timestamp: m.payload.timestamp });
  });
}

// ---------- connection ----------
let connecting: Promise<void> | null = null;

function connect(): Promise<void> {
  if (client) return Promise.resolve();
  if (!connecting) connecting = doConnect().finally(() => (connecting = null));
  return connecting;
}

async function doConnect(): Promise<void> {
  try {
    const c = await connectToDaemon({ target });
    client = c;
    c.subscribeConnectionStatus((state) => {
      emit({ type: "conn", state: (state as unknown as Json)?.status ?? state });
    });
    emit({ type: "connected", host: describeDaemonTarget(target) });
    await observeAgents(c);
    // older daemons may not stream workspaces; the TUI then derives them from agents
    await observeWorkspaces(c).catch((err) =>
      emit({ type: "warn", message: "workspaces unavailable: " + errMessage(err) }),
    );
    await observeSubagents(c).catch(() => {}); // older hosts: lists still work, just not live
    if (watched) await watch(c, watched.agentId, watched.tail);
  } catch (err) {
    client = null;
    emit({ type: "down", code: errCode(err), message: errMessage(err) });
  }
}

function need(): Client {
  if (!client) throw new Error("not connected to the daemon");
  return client;
}

const MIME: Record<string, string> = {
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".gif": "image/gif",
  ".webp": "image/webp",
};

async function readImages(paths: unknown): Promise<Array<{ data: string; mimeType: string }> | undefined> {
  if (!Array.isArray(paths) || paths.length === 0) return undefined;
  return Promise.all(
    paths.map(async (p: string) => ({
      data: (await readFile(p)).toString("base64"),
      mimeType: MIME[extname(p).toLowerCase()] ?? "image/png",
    })),
  );
}

// ---------- requests from the TUI ----------
async function handle(req: Json): Promise<unknown> {
  const agentId = req.agentId as string;
  if (connecting) await connecting;
  switch (req.op) {
    case "watch":
      await watch(need(), agentId, Number(req.tail ?? 400));
      return null;
    case "unwatch":
      watchGen++;
      watched = null;
      unwatch?.();
      unwatch = null;
      return null;
    case "send":
      return need().sendAgentMessage(agentId, req.text as string, {
        images: await readImages(req.images),
        ...(req.behavior ? { activeTurnBehavior: req.behavior as never } : {}),
      });
    case "subagents":
      return need().listProviderSubagents(agentId);
    case "subagentTimeline":
      return need().fetchProviderSubagentTimeline(agentId, req.subagentId as string);
    case "models":
      return need().listProviderModels(req.provider as string);
    case "setModel":
      return need().setAgentModel(agentId, (req.modelId as string) || null);
    case "setThinking":
      return need().setAgentThinkingOption(agentId, (req.optionId as string) || null);
    case "setMode":
      return need().setAgentMode(agentId, req.modeId as string);
    case "setFeature":
      return need().setAgentFeature(agentId, req.featureId as string, req.value);
    case "archiveWorkspace": {
      const r = (await need().archiveWorkspace(req.workspaceId as string)) as unknown as Json;
      if (r && r.error) throw new Error(String(r.error));
      return r;
    }
    case "removeProject":
      return need().removeProject(req.projectId as string);
    case "addProject": {
      const r = (await need().addProject(req.path as string)) as unknown as Json;
      if (r.error || !r.project) throw new Error(String(r.error ?? "could not add the project"));
      return r.project;
    }
    case "createProjectDirectory": {
      const r = (await need().createProjectDirectory({
        parentPath: req.parentPath as string,
        name: req.name as string,
      })) as unknown as Json;
      if (r.error || !r.project) throw new Error(String(r.error ?? "could not create the folder"));
      return r.project;
    }
    case "cloneProject": {
      const r = (await need().cloneGithubProject({
        repo: req.repo as string,
        targetDirectory: req.parentPath as string,
      })) as unknown as Json;
      if (r.error || !r.project) throw new Error(String(r.error ?? "clone failed"));
      return r.project;
    }
    case "branches": {
      const r = (await need().getBranchSuggestions({ cwd: req.cwd as string, limit: 100 })) as unknown as Json;
      if (r.error) throw new Error(String(r.error));
      return r.branches ?? [];
    }
    case "createWorkspace": {
      // one call creates the workspace and its first agent, as the desktop's New workspace does;
      // the daemon names the workspace and branch from the prompt
      const agent = req.agent as Json;
      const r = (await need().createWorkspace({
        source: req.source as never,
        agent: { ...agent, images: await readImages(agent.images) } as never,
        idempotencyKey: `paseo-tui:${Date.now()}:${Math.random().toString(36).slice(2)}`,
      })) as unknown as Json;
      if (r.error || !r.workspace) throw new Error(String(r.error ?? "could not create the workspace"));
      return { workspaceId: (r.workspace as Json).id, agentId: (r.agent as Json | null)?.id ?? null };
    }
    case "createAgent": {
      const agent = req.agent as Json;
      const a = (await need().createAgent({
        ...agent,
        workspaceId: req.workspaceId as string,
        images: await readImages(agent.images),
      } as never)) as unknown as Json;
      return { workspaceId: req.workspaceId, agentId: a.id };
    }
    case "forkContext": {
      // the desktop's fork: the source agent's chat history as one attachment for the new agent's first prompt
      const c = need();
      if (!c.getLastServerInfoMessage()?.features?.agentForkContext) {
        throw new Error("this daemon can't fork agents; update Paseo");
      }
      const r = await c.buildAgentForkContext(agentId);
      if (!r.attachment) throw new Error("the daemon returned no chat history");
      return { attachment: r.attachment, itemCount: r.itemCount };
    }
    case "history": {
      // the desktop's History: every agent, archived ones included, most recently active first
      const r = (await need().fetchAgentHistory({
        filter: { includeArchived: true },
        ...(req.search ? { search: req.search as string } : {}),
        sort: [{ key: "updated_at", direction: "desc" }],
        page: { limit: 100 },
      } as never)) as unknown as Json;
      return ((r.entries as Json[]) ?? []).map((e) => ({ ...(e.agent as Json), project: e.project ?? null }));
    }
    case "revive": {
      // bring an archived agent back: its workspace first (recreating a worktree), then the agent
      const c = need();
      const workspaceId = req.workspaceId as string;
      if (workspaceId && c.getLastServerInfoMessage()?.features?.workspaceRecovery) {
        const state = (await c.inspectWorkspaceRecovery(workspaceId)) as unknown as Json;
        if (state.kind === "recoverable") await c.restoreWorkspace(workspaceId);
      }
      if (req.archived) {
        try {
          await c.refreshAgent(agentId);
        } catch (err) {
          // the daemon unarchives before reloading; an agent that never ran has nothing to
          // reload, which is fine as long as it's back in the list
          for (let i = 0; i < 10 && !agents.get(agentId); i++) await sleep(100);
          const back = agents.get(agentId);
          if (!back || back.archivedAt) throw err;
        }
      }
      return null;
    }
    case "rename":
      return need().updateAgent(agentId, { name: req.name as string });
    case "commands": {
      // the agent's slash commands and skills, as the desktop's composer lists them
      const r = (await need().listCommands({ agentId })) as unknown as Json;
      if (r.error) throw new Error(String(r.error));
      const seen = new Set<string>();
      return ((r.commands as Json[]) ?? []).filter((c) => {
        const name = String(c.name);
        if (seen.has(name)) return false; // older daemons can list a command twice
        seen.add(name);
        return true;
      });
    }
    case "seen":
      return need().clearAgentAttention(agentId);
    case "cancel":
      return need().cancelAgent(agentId);
    case "archive":
      return need().archiveAgent(agentId);
    case "delete":
      return need().deleteAgent(agentId);
    case "permit":
      // a full response (a question's answers) when given, else plain allow / deny
      return need().respondToPermissionAndWait(
        agentId,
        req.requestId as string,
        (req.response ?? (req.allow ? { behavior: "allow" } : { behavior: "deny" })) as never,
      );
    case "archived":
      includeArchived = Boolean(req.value);
      await observeAgents(need());
      return null;
    case "providers": {
      const snap = await need().getProvidersSnapshot();
      return snap.entries.map((e) => ({
        provider: e.provider,
        label: e.label ?? e.provider,
        status: e.status === "ready" ? "available" : e.status,
        enabled: e.enabled ? "Enabled" : "Disabled",
        defaultMode: e.defaultModeId ?? "default",
      }));
    }
    case "usage": {
      // account quotas and balances, as the desktop's Usage panel; older hosts serve a cached list
      const { reports } = await need().listUsageReports({ forceRefresh: Boolean(req.refresh) || undefined });
      return reports.map(({ icon: _icon, ...r }) => r); // the SVG icons are for the desktop
    }
    case "run": {
      // same code path as `paseo run --no-wait`, including workspace/worktree setup
      const result = await runRunCommand(
        req.prompt as string,
        {
          daemonTarget: target,
          wait: false,
          title: (req.title as string) || undefined,
          provider: (req.provider as string) || undefined,
          model: (req.model as string) || undefined,
          mode: (req.mode as string) || undefined,
          cwd: (req.cwd as string) || undefined,
          newWorkspace: req.worktree ? "worktree" : req.newLocal ? "local" : undefined,
          workspace: (req.workspaceId as string) || undefined,
          image: Array.isArray(req.images) && req.images.length ? (req.images as string[]) : undefined,
        } as never,
        null as never,
      );
      return (result as unknown as { data: unknown }).data;
    }
    case "startDaemon": {
      if (target.kind !== "instance") throw new Error("can only start a local daemon");
      await runStart({ daemonTarget: target } as never, null as never);
      await connect();
      return null;
    }
    case "reconnect":
      await connect();
      return null;
    default:
      throw new Error(`unknown op ${String(req.op)}`);
  }
}

emit({ type: "hello", host: describeDaemonTarget(target), local: target.kind === "instance" });
void connect();

const rl = createInterface({ input: process.stdin });
rl.on("line", (line) => {
  let req: Json;
  try {
    req = JSON.parse(line);
  } catch {
    return;
  }
  handle(req).then(
    (data) => emit({ type: "res", id: req.id, ok: true, data: data ?? null }),
    (err) => emit({ type: "res", id: req.id, ok: false, error: errMessage(err) }),
  );
});
rl.on("close", () => {
  const done = () => process.exit(0);
  if (client) void client.close().finally(done);
  else done();
});
