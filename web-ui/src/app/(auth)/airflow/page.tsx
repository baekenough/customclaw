"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  ArrowUpDown,
  ChevronDown,
  ChevronRight,
  Info,
  Play,
  RefreshCw,
  Timer,
  Wind,
} from "lucide-react";

// ── Types ────────────────────────────────────────────────────────────────────

interface Dag {
  dag_id: string;
  is_paused: boolean;
  schedule_interval: string | null;
  last_run?: string;
  last_run_state?: string;
  tags?: { name: string }[];
  next_dagrun?: string | null;
  last_parsed_time?: string | null;
  description?: string | null;
}

interface DagRun {
  dag_run_id: string;
  state: string;
  start_date: string | null;
  end_date: string | null;
  logical_date: string;
  conf?: Record<string, unknown> | null;
  note?: string | null;
}

interface TaskInstance {
  task_id: string;
  state: string;
  start_date: string | null;
  end_date: string | null;
  duration: number | null;
}

interface ImportError {
  import_error_id: number;
  filename: string;
  stack_trace: string;
  timestamp: string;
}

// ── Sorting ──────────────────────────────────────────────────────────────────

type SortKey = "status" | "name" | "last_run" | "schedule";
type SortDir = "asc" | "desc";

interface SortState {
  key: SortKey;
  dir: SortDir;
}

const STATUS_PRIORITY: Record<string, number> = {
  failed: 0,
  upstream_failed: 0,
  running: 1,
  queued: 2,
  success: 3,
};

function getStatusPriority(state: string | undefined | null): number {
  if (!state) return 4; // never run → bottom
  return STATUS_PRIORITY[state] ?? 3;
}

function sortDags(dags: Dag[], sort: SortState): Dag[] {
  return [...dags].sort((a, b) => {
    let cmp = 0;
    switch (sort.key) {
      case "status": {
        const pa = getStatusPriority(a.last_run_state);
        const pb = getStatusPriority(b.last_run_state);
        cmp = pa - pb;
        // Within same status, sort by last_run descending (most recent first)
        if (cmp === 0) {
          const ta = a.last_run ? new Date(a.last_run).getTime() : 0;
          const tb = b.last_run ? new Date(b.last_run).getTime() : 0;
          cmp = tb - ta;
        }
        break;
      }
      case "name":
        cmp = a.dag_id.localeCompare(b.dag_id);
        break;
      case "last_run": {
        const ta = a.last_run ? new Date(a.last_run).getTime() : 0;
        const tb = b.last_run ? new Date(b.last_run).getTime() : 0;
        cmp = tb - ta; // default: newest first
        break;
      }
      case "schedule":
        cmp = (a.schedule_interval ?? "").localeCompare(
          b.schedule_interval ?? ""
        );
        break;
    }
    return sort.dir === "asc" ? cmp : -cmp;
  });
}

// ── Sort Header Button ────────────────────────────────────────────────────────

function SortHeaderBtn({
  label,
  sortKey,
  current,
  onSort,
  className,
}: {
  label: string;
  sortKey: SortKey;
  current: SortState;
  onSort: (key: SortKey) => void;
  className?: string;
}) {
  const isActive = current.key === sortKey;
  const Icon = isActive
    ? current.dir === "asc"
      ? ArrowUp
      : ArrowDown
    : ArrowUpDown;

  return (
    <button
      type="button"
      onClick={() => onSort(sortKey)}
      className={`inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors select-none ${className ?? ""}`}
      aria-label={`${label} 기준 정렬`}
    >
      {label}
      <Icon
        className={`h-3 w-3 ${isActive ? "text-foreground" : "text-muted-foreground/50"}`}
        aria-hidden="true"
      />
    </button>
  );
}

// ── Helpers ──────────────────────────────────────────────────────────────────

const RUN_STATE_CLASSES: Record<string, string> = {
  success: "bg-emerald-500/15 text-emerald-400 border-0",
  failed: "bg-destructive/15 text-destructive border-0",
  running: "bg-blue-500/15 text-blue-400 border-0",
  queued: "bg-yellow-500/15 text-yellow-400 border-0",
};

function StateBadge({ state }: { state?: string }) {
  if (!state) return <span className="text-sm text-muted-foreground">—</span>;
  return (
    <Badge
      variant="outline"
      className={`text-xs ${RUN_STATE_CLASSES[state] ?? "text-muted-foreground"}`}
    >
      {state}
    </Badge>
  );
}

function formatDuration(startDate: string | null, endDate: string | null, durationSec: number | null): string {
  if (durationSec != null) {
    if (durationSec < 60) return `${Math.round(durationSec)}s`;
    const m = Math.floor(durationSec / 60);
    const s = Math.round(durationSec % 60);
    return s > 0 ? `${m}m${s}s` : `${m}m`;
  }
  if (startDate && endDate) {
    const ms = new Date(endDate).getTime() - new Date(startDate).getTime();
    const sec = Math.round(ms / 1000);
    if (sec < 60) return `${sec}s`;
    const m = Math.floor(sec / 60);
    const s = sec % 60;
    return s > 0 ? `${m}m${s}s` : `${m}m`;
  }
  if (startDate && !endDate) {
    const elapsed = Math.round((Date.now() - new Date(startDate).getTime()) / 1000);
    if (elapsed < 60) return `${elapsed}s...`;
    return `${Math.floor(elapsed / 60)}m...`;
  }
  return "—";
}

function shortRunId(runId: string): string {
  // web_ui_1234567890 → web_ui_1234...  or  scheduled__2024-01-01T00:00:00+00:00 → scheduled__01-01
  const parts = runId.split("__");
  if (parts.length >= 2) {
    const ts = parts[1].slice(0, 10); // date only
    return `${parts[0]}__${ts}`;
  }
  return runId.length > 24 ? runId.slice(0, 22) + "…" : runId;
}

function relativeTime(dateStr: string): string {
  const now = Date.now();
  const then = new Date(dateStr).getTime();
  const diffSec = Math.floor((now - then) / 1000);
  if (diffSec < 60) return "방금 전";
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}분 전`;
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}시간 전`;
  if (diffSec < 172800) return "어제";
  return `${Math.floor(diffSec / 86400)}일 전`;
}

function formatRunDate(dateStr: string | null): string {
  if (!dateStr) return "—";
  const date = new Date(dateStr);
  const now = new Date();
  const isToday =
    date.getFullYear() === now.getFullYear() &&
    date.getMonth() === now.getMonth() &&
    date.getDate() === now.getDate();
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  const isYesterday =
    date.getFullYear() === yesterday.getFullYear() &&
    date.getMonth() === yesterday.getMonth() &&
    date.getDate() === yesterday.getDate();

  const hh = date.getHours().toString().padStart(2, "0");
  const mm = date.getMinutes().toString().padStart(2, "0");
  if (isToday) return `${hh}:${mm}`;
  if (isYesterday) return `어제 ${hh}:${mm}`;
  const mo = (date.getMonth() + 1).toString().padStart(2, "0");
  const dd = date.getDate().toString().padStart(2, "0");
  return `${mo}/${dd} ${hh}:${mm}`;
}

// ── Summary Stats Bar ─────────────────────────────────────────────────────────

function SummaryStatsBar({ dags }: { dags: Dag[] }) {
  const total = dags.length;
  const active = dags.filter((d) => !d.is_paused).length;
  const running = dags.filter((d) => d.last_run_state === "running").length;
  const failed = dags.filter(
    (d) => d.last_run_state === "failed" || d.last_run_state === "upstream_failed"
  ).length;

  const stats = [
    {
      label: "총 DAG",
      value: total,
      dotClass: "bg-muted-foreground",
      valueClass: "text-foreground",
    },
    {
      label: "활성",
      value: active,
      dotClass: "bg-emerald-400",
      valueClass: "text-emerald-400",
    },
    {
      label: "실행중",
      value: running,
      dotClass: "bg-blue-400",
      valueClass: "text-blue-400",
    },
    {
      label: "실패",
      value: failed,
      dotClass: "bg-destructive",
      valueClass: "text-destructive",
    },
  ];

  return (
    <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
      {stats.map((s) => (
        <div
          key={s.label}
          className="flex items-center gap-3 rounded-lg border border-border/40 bg-card px-4 py-3"
        >
          <span className={`h-2 w-2 rounded-full shrink-0 ${s.dotClass}`} aria-hidden="true" />
          <div>
            <p className="text-xs text-muted-foreground">{s.label}</p>
            <p className={`text-xl font-bold leading-none mt-0.5 ${s.valueClass}`}>
              {s.value}
            </p>
          </div>
        </div>
      ))}
    </div>
  );
}

// ── Sub-components ───────────────────────────────────────────────────────────

function TaskList({ dagId, runId }: { dagId: string; runId: string }) {
  const [tasks, setTasks] = useState<TaskInstance[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetch(`/api/airflow/dags/${dagId}/runs/${encodeURIComponent(runId)}/tasks`)
      .then((r) => (r.ok ? r.json() : Promise.reject()))
      .then((data) => setTasks(data.task_instances ?? []))
      .catch(() => setTasks([]))
      .finally(() => setLoading(false));
  }, [dagId, runId]);

  if (loading) {
    return (
      <div className="pl-4 py-2 space-y-1">
        {[1, 2, 3].map((i) => (
          <div key={i} className="h-5 w-full animate-pulse rounded bg-muted" />
        ))}
      </div>
    );
  }

  if (tasks.length === 0) {
    return (
      <p className="pl-4 py-2 text-xs text-muted-foreground">태스크 없음</p>
    );
  }

  return (
    <div className="pl-4 py-1 space-y-px">
      {tasks.map((task, idx) => (
        <div
          key={task.task_id}
          className="flex items-center gap-2 py-1 text-xs"
        >
          <span className="text-muted-foreground/60 w-3 shrink-0 text-right">
            {idx === tasks.length - 1 ? "└" : "├"}
          </span>
          <span className="font-mono text-foreground/80 truncate flex-1 min-w-0">
            {task.task_id}
          </span>
          <StateBadge state={task.state} />
          <span className="text-muted-foreground w-12 text-right shrink-0">
            {formatDuration(task.start_date, task.end_date, task.duration)}
          </span>
        </div>
      ))}
    </div>
  );
}

function RunRow({
  dagId,
  run,
}: {
  dagId: string;
  run: DagRun;
}) {
  const [expanded, setExpanded] = useState(false);

  // Extract notable conf params
  const issueNumber =
    run.conf && typeof run.conf.issue_number !== "undefined"
      ? run.conf.issue_number
      : null;

  return (
    <div>
      <button
        type="button"
        onClick={() => setExpanded((p) => !p)}
        className="w-full flex items-center gap-2 py-1.5 px-2 rounded hover:bg-muted/30 transition-colors text-left"
        aria-expanded={expanded}
        aria-label={`${shortRunId(run.dag_run_id)} 태스크 ${expanded ? "접기" : "펼치기"}`}
      >
        {expanded ? (
          <ChevronDown className="h-3 w-3 text-muted-foreground shrink-0" />
        ) : (
          <ChevronRight className="h-3 w-3 text-muted-foreground shrink-0" />
        )}
        <span className="font-mono text-xs text-foreground/80 truncate flex-1 min-w-0">
          {shortRunId(run.dag_run_id)}
        </span>
        {issueNumber !== null && (
          <span className="text-xs font-mono bg-muted/50 text-foreground/70 px-1.5 py-0.5 rounded shrink-0">
            #{String(issueNumber)}
          </span>
        )}
        <StateBadge state={run.state} />
        <span className="text-xs text-muted-foreground shrink-0 hidden sm:block w-20 text-right tabular-nums">
          {formatRunDate(run.start_date)}
        </span>
        <span className="text-xs text-muted-foreground w-14 text-right shrink-0 flex items-center justify-end gap-1">
          <Timer className="h-3 w-3" />
          {formatDuration(run.start_date, run.end_date, null)}
        </span>
      </button>
      {expanded && <TaskList dagId={dagId} runId={run.dag_run_id} />}
    </div>
  );
}

function DagRunsPanel({
  dagId,
  onTrigger,
}: {
  dagId: string;
  onTrigger: () => void;
}) {
  const [runs, setRuns] = useState<DagRun[]>([]);
  const [loading, setLoading] = useState(true);
  const [triggering, setTriggering] = useState(false);

  useEffect(() => {
    fetch(`/api/airflow/dags/${dagId}/runs`)
      .then((r) => (r.ok ? r.json() : Promise.reject()))
      .then((data) => setRuns((data.dag_runs ?? []).slice(0, 10)))
      .catch(() => setRuns([]))
      .finally(() => setLoading(false));
  }, [dagId]);

  const handleTrigger = async () => {
    setTriggering(true);
    try {
      const res = await fetch(`/api/airflow/dags/${dagId}/trigger`, {
        method: "POST",
      });
      if (res.ok) {
        onTrigger();
        // Reload runs after a short delay to pick up the new run
        setTimeout(() => {
          setLoading(true);
          fetch(`/api/airflow/dags/${dagId}/runs`)
            .then((r) => (r.ok ? r.json() : Promise.reject()))
            .then((data) => setRuns((data.dag_runs ?? []).slice(0, 10)))
            .catch(() => {})
            .finally(() => setLoading(false));
        }, 1500);
      }
    } finally {
      setTriggering(false);
    }
  };

  return (
    <div className="border-t border-border/30 bg-muted/10">
      <div className="px-3 py-2 flex items-center justify-between">
        <span className="text-xs font-medium text-muted-foreground">최근 실행</span>
        <Button
          variant="outline"
          size="sm"
          className="h-6 text-xs gap-1 px-2"
          onClick={handleTrigger}
          disabled={triggering}
          aria-label={`${dagId} DAG 수동 실행`}
        >
          <Play className="h-3 w-3" />
          {triggering ? "실행 중..." : "실행"}
        </Button>
      </div>
      {loading ? (
        <div className="px-3 pb-3 space-y-1.5">
          {[1, 2, 3].map((i) => (
            <div key={i} className="h-6 w-full animate-pulse rounded bg-muted" />
          ))}
        </div>
      ) : runs.length === 0 ? (
        <p className="px-3 pb-3 text-xs text-muted-foreground">실행 이력 없음</p>
      ) : (
        <div className="px-3 pb-2 space-y-px">
          {runs.map((run) => (
            <RunRow key={run.dag_run_id} dagId={dagId} run={run} />
          ))}
        </div>
      )}
    </div>
  );
}

function DagRow({ dag }: { dag: Dag }) {
  const [expanded, setExpanded] = useState(false);
  const [triggerCount, setTriggerCount] = useState(0);

  return (
    <div className="border-b border-border/30 last:border-0">
      <button
        type="button"
        onClick={() => setExpanded((p) => !p)}
        className="w-full flex items-center gap-3 px-4 py-3 hover:bg-muted/20 transition-colors text-left"
        aria-expanded={expanded}
        aria-label={`${dag.dag_id} ${expanded ? "접기" : "펼치기"}`}
      >
        {expanded ? (
          <ChevronDown className="h-4 w-4 text-muted-foreground shrink-0" />
        ) : (
          <ChevronRight className="h-4 w-4 text-muted-foreground shrink-0" />
        )}
        <span className="font-mono text-sm truncate flex-1 min-w-0 flex items-center gap-1.5">
          {dag.dag_id}
          {dag.description && (
            <span title={dag.description} className="shrink-0 inline-flex">
              <Info
                className="h-3 w-3 text-muted-foreground/50"
                aria-hidden="true"
              />
            </span>
          )}
        </span>
        {/* Tags */}
        {dag.tags && dag.tags.length > 0 && (
          <div className="hidden sm:flex items-center gap-1 shrink-0">
            {dag.tags.slice(0, 3).map((tag) => (
              <span
                key={tag.name}
                className="text-xs bg-muted/50 text-muted-foreground px-1.5 py-0.5 rounded"
              >
                {tag.name}
              </span>
            ))}
            {dag.tags.length > 3 && (
              <span className="text-xs text-muted-foreground/60">
                +{dag.tags.length - 3}
              </span>
            )}
          </div>
        )}
        <Badge
          variant="outline"
          className={`text-xs shrink-0 ${
            dag.is_paused
              ? "text-muted-foreground"
              : "bg-emerald-500/15 text-emerald-400 border-0"
          }`}
        >
          {dag.is_paused ? "일시정지" : "활성"}
        </Badge>
        <span className="text-xs text-muted-foreground font-mono shrink-0 hidden sm:block w-20 text-right">
          {dag.schedule_interval ? String(dag.schedule_interval) : "—"}
        </span>
        {/* Last run time relative */}
        {dag.last_run && (
          <span className="text-xs text-muted-foreground shrink-0 hidden sm:block w-14 text-right tabular-nums">
            {relativeTime(dag.last_run)}
          </span>
        )}
        <div className="shrink-0">
          <StateBadge state={dag.last_run_state} />
        </div>
      </button>
      {expanded && (
        <DagRunsPanel
          dagId={dag.dag_id}
          onTrigger={() => setTriggerCount((c) => c + 1)}
          key={triggerCount}
        />
      )}
    </div>
  );
}

// ── Main Page ────────────────────────────────────────────────────────────────

export default function AirflowPage() {
  const [dags, setDags] = useState<Dag[]>([]);
  const [importErrors, setImportErrors] = useState<ImportError[]>([]);
  const [loading, setLoading] = useState(true);
  const [airflowError, setAirflowError] = useState<string | null>(null);
  const [autoRefresh, setAutoRefresh] = useState(false);
  const [sort, setSort] = useState<SortState>({ key: "status", dir: "asc" });
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const handleSort = useCallback((key: SortKey) => {
    setSort((prev) =>
      prev.key === key
        ? { key, dir: prev.dir === "asc" ? "desc" : "asc" }
        : { key, dir: "asc" }
    );
  }, []);

  const sortedDags = useMemo(() => sortDags(dags, sort), [dags, sort]);

  const fetchData = useCallback(() => {
    setLoading(true);
    setAirflowError(null);

    Promise.all([
      fetch("/api/airflow/dags")
        .then((r) => (r.ok ? r.json() : Promise.reject(r.statusText)))
        .then((data) => setDags(data.dags ?? []))
        .catch(() => {
          setDags([]);
          setAirflowError("Airflow에 연결하지 못했습니다");
        }),
      fetch("/api/airflow/import-errors")
        .then((r) => (r.ok ? r.json() : Promise.reject()))
        .then((data) => setImportErrors(data.import_errors ?? []))
        .catch(() => setImportErrors([])),
    ]).finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  useEffect(() => {
    if (autoRefresh) {
      intervalRef.current = setInterval(fetchData, 30_000);
    } else {
      if (intervalRef.current) clearInterval(intervalRef.current);
    }
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current);
    };
  }, [autoRefresh, fetchData]);

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Airflow DAGs</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Apache Airflow 워크플로우 현황
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant={autoRefresh ? "default" : "outline"}
            size="sm"
            onClick={() => setAutoRefresh((p) => !p)}
            className="gap-2 text-xs"
            aria-pressed={autoRefresh}
            aria-label="30초 자동 새로고침 토글"
          >
            <Timer className="h-3.5 w-3.5" />
            {autoRefresh ? "자동 켜짐" : "30초 자동"}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={fetchData}
            className="gap-2"
            aria-label="새로고침"
          >
            <RefreshCw className="h-4 w-4" />
            새로고침
          </Button>
        </div>
      </div>

      {/* Summary Stats Bar */}
      {!loading && !airflowError && dags.length > 0 && (
        <SummaryStatsBar dags={dags} />
      )}

      {/* Import Errors */}
      {importErrors.length > 0 && (
        <Card className="border-destructive/30 bg-destructive/5">
          <CardHeader className="pb-3">
            <div className="flex items-center gap-2">
              <AlertTriangle className="h-4 w-4 text-destructive" />
              <CardTitle className="text-sm text-destructive">
                Import 오류 ({importErrors.length}개)
              </CardTitle>
            </div>
          </CardHeader>
          <CardContent className="space-y-3">
            {importErrors.map((err) => (
              <div
                key={err.import_error_id}
                className="rounded-lg bg-muted/30 p-3 space-y-1"
              >
                <p className="text-xs font-mono font-medium text-foreground">
                  {err.filename}
                </p>
                <pre className="text-xs text-muted-foreground whitespace-pre-wrap font-mono overflow-auto max-h-24">
                  {err.stack_trace}
                </pre>
                <p className="text-xs text-muted-foreground">
                  {new Date(err.timestamp).toLocaleString("ko-KR")}
                </p>
              </div>
            ))}
          </CardContent>
        </Card>
      )}

      {/* DAG List */}
      <Card className="border-border/50">
        <CardHeader className="pb-3">
          <CardTitle className="text-base">DAG 목록</CardTitle>
          <CardDescription className="text-xs">
            {loading
              ? "로딩 중..."
              : `총 ${dags.length}개의 DAG · 클릭하면 실행 이력을 볼 수 있습니다`}
          </CardDescription>
        </CardHeader>
        <CardContent className="px-0 pb-0">
          {airflowError ? (
            <div className="flex flex-col items-center justify-center py-16 text-center px-6">
              <Wind className="h-12 w-12 text-muted-foreground/30 mb-4" />
              <p className="text-sm text-muted-foreground">{airflowError}</p>
              <p className="text-xs text-muted-foreground/60 mt-1">
                Airflow 서비스가 실행 중인지 확인하세요
              </p>
            </div>
          ) : loading ? (
            <div className="space-y-px px-4 pb-4">
              {[1, 2, 3, 4].map((i) => (
                <div
                  key={i}
                  className="h-11 w-full animate-pulse rounded bg-muted"
                />
              ))}
            </div>
          ) : dags.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center px-6">
              <Wind className="h-12 w-12 text-muted-foreground/30 mb-4" />
              <p className="text-sm text-muted-foreground">DAG이 없습니다</p>
            </div>
          ) : (
            <div>
              {/* Column sort headers */}
              <div className="flex items-center gap-3 px-4 py-2 border-b border-border/20 bg-muted/5">
                <span className="w-4 shrink-0" aria-hidden="true" />
                <SortHeaderBtn
                  label="이름"
                  sortKey="name"
                  current={sort}
                  onSort={handleSort}
                  className="flex-1 min-w-0 justify-start"
                />
                <span className="hidden sm:block w-16 shrink-0" />
                <span className="hidden sm:block w-14 shrink-0" />
                <SortHeaderBtn
                  label="스케줄"
                  sortKey="schedule"
                  current={sort}
                  onSort={handleSort}
                  className="hidden sm:flex w-20 shrink-0 justify-end"
                />
                <SortHeaderBtn
                  label="최근 실행"
                  sortKey="last_run"
                  current={sort}
                  onSort={handleSort}
                  className="hidden sm:flex w-14 shrink-0 justify-end"
                />
                <SortHeaderBtn
                  label="상태"
                  sortKey="status"
                  current={sort}
                  onSort={handleSort}
                  className="w-16 shrink-0 justify-end"
                />
              </div>
              {sortedDags.map((dag) => (
                <DagRow key={dag.dag_id} dag={dag} />
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
