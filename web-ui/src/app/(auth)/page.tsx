"use client";

import { useCallback, useEffect, useState } from "react";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import {
  Database,
  Layers,
  Search,
  Wind,
  Bot,
  MessageSquare,
  DollarSign,
  RefreshCw,
  Coins,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  AreaChart,
  Area,
} from "recharts";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface AirflowHealthDetail {
  status: boolean;
  metadatabase?: boolean;
  scheduler?: boolean;
  dagProcessor?: boolean;
}

interface LlmProviderStatus {
  provider: string;
  status: string;
  error: string | null;
  checkedAt: string;
}

interface HealthStatus {
  postgres: boolean;
  redis: boolean;
  opensearch: boolean;
  airflow: AirflowHealthDetail | boolean;
  healthy: boolean;
  llmProviders?: LlmProviderStatus[];
}

interface DagSummary {
  totalDags: number;
  activeDags: number;
  failedRuns: number;
  runningRuns: number;
}

interface UsageData {
  totalMessages: number;
  botMessages: number;
  totalCostUsd: number;
  totalTokens: number;
  tokenBreakdown?: {
    input: number;
    output: number;
    cacheRead: number;
    cacheCreation: number;
  };
  stats: Array<{
    botId: string;
    botName: string;
    model: string;
    calls: number;
    totalTokens: number;
    costUsd: number;
  }>;
  daily: Array<{
    date: string;
    messages: number;
    botMessages: number;
    tokens: number;
    costUsd: number;
  }>;
  botCalls: Array<{
    botId: string;
    botName: string;
    received: number;
    sent: number;
  }>;
}

type Period = "day" | "week" | "month" | "all";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const SERVICE_ICONS = {
  postgres: Database,
  redis: Layers,
  opensearch: Search,
} as const;

const PROVIDER_CONFIG: Record<string, { label: string; icon: typeof Bot }> = {
  claude: { label: "Claude (Anthropic)", icon: Bot },
  openai: { label: "OpenAI", icon: Bot },
  gemini: { label: "Gemini (Google)", icon: Bot },
};

const SERVICE_LABELS = {
  postgres: "PostgreSQL",
  redis: "Redis",
  opensearch: "OpenSearch",
} as const;

const PERIODS = [
  { label: "일", value: "day" },
  { label: "주", value: "week" },
  { label: "월", value: "month" },
  { label: "전체", value: "all" },
] as const;

const CHART_COLORS = {
  primary: "#3b82f6",
  secondary: "#10b981",
  tertiary: "#f59e0b",
} as const;

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function computeDagSummary(
  dags: Array<{ is_paused: boolean; last_run_state?: string }>,
): DagSummary {
  const activeDags = dags.filter((d) => !d.is_paused).length;
  const failedRuns = dags.filter((d) => d.last_run_state === "failed").length;
  const runningRuns = dags.filter((d) => d.last_run_state === "running").length;
  return { totalDags: dags.length, activeDags, failedRuns, runningRuns };
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return n.toLocaleString();
}

function formatCost(n: number): string {
  return `$${n.toFixed(4)}`;
}

function formatChartDate(dateStr: string): string {
  const d = new Date(dateStr);
  return `${d.getMonth() + 1}/${d.getDate()}`;
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function ChartSkeleton() {
  return (
    <div className="flex h-[300px] items-center justify-center">
      <div className="space-y-3 w-full px-6">
        <div className="h-4 w-24 animate-pulse rounded bg-muted" />
        <div className="flex items-end gap-2 h-[220px]">
          {Array.from({ length: 8 }).map((_, i) => (
            <div
              key={i}
              className="flex-1 animate-pulse rounded-t bg-muted"
              style={{ height: `${30 + Math.random() * 60}%` }}
            />
          ))}
        </div>
        <div className="h-3 w-full animate-pulse rounded bg-muted" />
      </div>
    </div>
  );
}

function EmptyChart({ message }: { message: string }) {
  return (
    <div className="flex h-[300px] items-center justify-center text-sm text-muted-foreground">
      {message}
    </div>
  );
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function CustomTooltip({ active, payload, label }: any) {
  if (!active || !payload?.length) return null;
  return (
    <div className="rounded-lg border border-border/50 bg-background px-3 py-2 text-xs shadow-xl">
      <p className="mb-1 font-medium text-foreground">{label}</p>
      {payload.map((entry: { name: string; value: number; color: string }) => (
        <div key={entry.name} className="flex items-center gap-2">
          <span
            className="inline-block h-2 w-2 rounded-full"
            style={{ backgroundColor: entry.color }}
          />
          <span className="text-muted-foreground">{entry.name}:</span>
          <span className="font-medium text-foreground">
            {entry.value.toLocaleString()}
          </span>
        </div>
      ))}
    </div>
  );
}

function PeriodToggle({
  value,
  globalValue,
  onChange,
}: {
  value: Period | null;
  globalValue: Period;
  onChange: (p: Period | null) => void;
}) {
  const active = value ?? globalValue;
  return (
    <div className="inline-flex items-center rounded-md bg-muted/50 p-0.5 gap-0.5">
      {PERIODS.map((p) => (
        <button
          key={p.value}
          onClick={() => onChange(p.value === globalValue ? null : p.value)}
          className={`px-2 py-0.5 text-[10px] font-medium rounded transition-all ${
            active === p.value
              ? "bg-background text-foreground shadow-sm"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          {p.label}
        </button>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

export default function DashboardPage() {
  // --- Service health state ---
  const [health, setHealth] = useState<HealthStatus | null>(null);
  const [dagSummary, setDagSummary] = useState<DagSummary | null>(null);
  const [loadingHealth, setLoadingHealth] = useState(true);
  const [loadingDags, setLoadingDags] = useState(true);

  // --- Usage / stats state ---
  const [period, setPeriod] = useState<Period>("month");
  const [usage, setUsage] = useState<UsageData | null>(null);
  const [loadingUsage, setLoadingUsage] = useState(true);

  // --- Per-card period overrides (null = follow global) ---
  const [botsPeriod, setBotsPeriod] = useState<Period | null>(null);
  const [chartPeriod, setChartPeriod] = useState<Period | null>(null);
  const [botsUsage, setBotsUsage] = useState<UsageData | null>(null);
  const [chartUsage, setChartUsage] = useState<UsageData | null>(null);
  const [loadingBots, setLoadingBots] = useState(false);
  const [loadingChart, setLoadingChart] = useState(false);

  // --- Fetch health (independent of period) ---
  const fetchHealth = useCallback(() => {
    setLoadingHealth(true);
    setLoadingDags(true);

    fetch("/api/health")
      .then((r) => r.json())
      .then(setHealth)
      .catch(() => setHealth(null))
      .finally(() => setLoadingHealth(false));

    fetch("/api/airflow/dags")
      .then((r) => (r.ok ? r.json() : Promise.reject()))
      .then((data) => setDagSummary(computeDagSummary(data.dags ?? [])))
      .catch(() => setDagSummary(null))
      .finally(() => setLoadingDags(false));
  }, []);

  // --- Fetch usage (period-aware) ---
  const fetchUsage = useCallback((p: Period) => {
    setLoadingUsage(true);
    fetch(`/api/usage?period=${p}`)
      .then((r) => r.json())
      .then(setUsage)
      .catch(() => setUsage(null))
      .finally(() => setLoadingUsage(false));
  }, []);

  // --- Per-card fetch functions ---
  const fetchBotsData = useCallback((p: Period) => {
    setLoadingBots(true);
    fetch(`/api/usage?period=${p}`)
      .then((r) => r.json())
      .then(setBotsUsage)
      .catch(() => setBotsUsage(null))
      .finally(() => setLoadingBots(false));
  }, []);

  const fetchChartData = useCallback((p: Period) => {
    setLoadingChart(true);
    fetch(`/api/usage?period=${p}`)
      .then((r) => r.json())
      .then(setChartUsage)
      .catch(() => setChartUsage(null))
      .finally(() => setLoadingChart(false));
  }, []);

  // --- Initial load ---
  useEffect(() => {
    fetchHealth();
    fetchUsage(period);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // --- Re-fetch usage when period changes ---
  const handlePeriodChange = (p: Period) => {
    setPeriod(p);
    fetchUsage(p);
    // Reset card overrides that match the new global
    if (botsPeriod === p) {
      setBotsPeriod(null);
      setBotsUsage(null);
    }
    if (chartPeriod === p) {
      setChartPeriod(null);
      setChartUsage(null);
    }
  };

  const handleRefresh = () => {
    fetchHealth();
    fetchUsage(period);
  };

  // --- Derived chart data ---
  const effectiveBotData =
    (botsPeriod !== null ? botsUsage : usage)?.botCalls ?? [];
  const effectiveDailyData = (
    (chartPeriod !== null ? chartUsage : usage)?.daily ?? []
  ).map((d) => ({
    ...d,
    dateLabel: formatChartDate(d.date),
  }));
  const isBotsLoading = botsPeriod !== null ? loadingBots : loadingUsage;
  const isChartLoading = chartPeriod !== null ? loadingChart : loadingUsage;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">대시보드</h1>
          <p className="text-sm text-muted-foreground mt-1">
            시스템 상태 및 사용 통계
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={handleRefresh}
          className="gap-2"
        >
          <RefreshCw className="h-4 w-4" />
          새로고침
        </Button>
      </div>

      {/* ================================================================= */}
      {/* Service Health (unchanged)                                        */}
      {/* ================================================================= */}
      <section>
        <h2 className="text-sm font-semibold text-muted-foreground uppercase tracking-wide mb-3">
          서비스 상태
        </h2>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          {(
            Object.entries(SERVICE_LABELS) as [
              keyof typeof SERVICE_LABELS,
              string,
            ][]
          ).map(([key, label]) => {
            const Icon = SERVICE_ICONS[key];
            const status = health ? health[key] : null;
            return (
              <Card key={key} className="border-border/50">
                <CardContent className="flex items-center gap-3 p-4">
                  <div
                    className={`flex h-9 w-9 items-center justify-center rounded-lg ${
                      status === null
                        ? "bg-muted"
                        : status
                          ? "bg-emerald-500/15"
                          : "bg-destructive/15"
                    }`}
                  >
                    <Icon
                      className={`h-4 w-4 ${
                        status === null
                          ? "text-muted-foreground"
                          : status
                            ? "text-emerald-500"
                            : "text-destructive"
                      }`}
                    />
                  </div>
                  <div className="min-w-0">
                    <p className="text-xs text-muted-foreground">{label}</p>
                    {loadingHealth ? (
                      <div className="mt-1 h-4 w-12 animate-pulse rounded bg-muted" />
                    ) : (
                      <Badge
                        variant={status ? "default" : "destructive"}
                        className={`mt-0.5 text-xs ${
                          status
                            ? "bg-emerald-500/15 text-emerald-400 hover:bg-emerald-500/20 border-0"
                            : ""
                        }`}
                      >
                        {status ? "정상" : "오류"}
                      </Badge>
                    )}
                  </div>
                </CardContent>
              </Card>
            );
          })}

          {/* Airflow card */}
          <Card className="border-border/50">
            <CardContent className="flex items-center gap-3 p-4">
              {(() => {
                const airflowStatus =
                  health === null
                    ? null
                    : typeof health.airflow === "object"
                      ? health.airflow.status
                      : !!health.airflow;
                const airflowDetail =
                  health !== null && typeof health.airflow === "object"
                    ? health.airflow
                    : null;

                return (
                  <>
                    <div
                      className={`flex h-9 w-9 items-center justify-center rounded-lg ${
                        airflowStatus === null
                          ? "bg-muted"
                          : airflowStatus
                            ? "bg-emerald-500/15"
                            : "bg-destructive/15"
                      }`}
                    >
                      <Wind
                        className={`h-4 w-4 ${
                          airflowStatus === null
                            ? "text-muted-foreground"
                            : airflowStatus
                              ? "text-emerald-500"
                              : "text-destructive"
                        }`}
                      />
                    </div>
                    <div className="min-w-0 flex-1">
                      <p className="text-xs text-muted-foreground">Airflow</p>
                      {loadingHealth ? (
                        <div className="mt-1 h-4 w-20 animate-pulse rounded bg-muted" />
                      ) : airflowDetail !== null ? (
                        <div className="mt-0.5 flex flex-wrap gap-1">
                          {airflowDetail.metadatabase === false && (
                            <Badge
                              variant="destructive"
                              className="text-xs border-0"
                            >
                              DB 오류
                            </Badge>
                          )}
                          {airflowDetail.scheduler === false && (
                            <Badge
                              variant="destructive"
                              className="text-xs border-0"
                            >
                              스케줄러 오류
                            </Badge>
                          )}
                          {airflowDetail.dagProcessor === false && (
                            <Badge
                              variant="destructive"
                              className="text-xs border-0"
                            >
                              DAG 프로세서 오류
                            </Badge>
                          )}
                          {airflowDetail.status && (
                            <Badge className="text-xs bg-emerald-500/15 text-emerald-400 hover:bg-emerald-500/20 border-0">
                              정상
                            </Badge>
                          )}
                        </div>
                      ) : airflowStatus === false ? (
                        <Badge variant="destructive" className="mt-0.5 text-xs">
                          오류
                        </Badge>
                      ) : (
                        <Badge className="mt-0.5 text-xs bg-emerald-500/15 text-emerald-400 hover:bg-emerald-500/20 border-0">
                          정상
                        </Badge>
                      )}
                      {!loadingDags && dagSummary && (
                        <div className="mt-1 flex flex-wrap gap-1">
                          <Badge className="text-xs bg-muted/60 text-muted-foreground border-0">
                            {dagSummary.activeDags}개 활성
                          </Badge>
                          {dagSummary.runningRuns > 0 && (
                            <Badge className="text-xs bg-blue-500/15 text-blue-400 border-0">
                              {dagSummary.runningRuns} 실행중
                            </Badge>
                          )}
                          {dagSummary.failedRuns > 0 && (
                            <Badge className="text-xs bg-orange-500/15 text-orange-400 border-0">
                              {dagSummary.failedRuns} 실패
                            </Badge>
                          )}
                        </div>
                      )}
                    </div>
                  </>
                );
              })()}
            </CardContent>
          </Card>
        </div>
      </section>

      {/* ================================================================= */}
      {/* LLM Provider Status                                               */}
      {/* ================================================================= */}
      {health?.llmProviders && health.llmProviders.length > 0 && (
        <section>
          <h2 className="text-sm font-semibold text-muted-foreground uppercase tracking-wide mb-3">
            LLM 프로바이더
          </h2>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
            {health.llmProviders.map((p) => {
              const config = PROVIDER_CONFIG[p.provider] ?? {
                label: p.provider,
                icon: Bot,
              };
              const Icon = config.icon;
              const isOk = p.status === "ok";
              const isUnconfigured = p.status === "unconfigured";
              return (
                <Card key={p.provider} className="border-border/50">
                  <CardContent className="flex items-center gap-3 p-4">
                    <div
                      className={`flex h-9 w-9 items-center justify-center rounded-lg ${
                        isUnconfigured
                          ? "bg-muted"
                          : isOk
                            ? "bg-emerald-500/15"
                            : "bg-destructive/15"
                      }`}
                    >
                      <Icon
                        className={`h-4 w-4 ${
                          isUnconfigured
                            ? "text-muted-foreground"
                            : isOk
                              ? "text-emerald-500"
                              : "text-destructive"
                        }`}
                      />
                    </div>
                    <div className="min-w-0 flex-1">
                      <p className="text-xs text-muted-foreground">
                        {config.label}
                      </p>
                      {loadingHealth ? (
                        <div className="mt-1 h-4 w-12 animate-pulse rounded bg-muted" />
                      ) : (
                        <>
                          <Badge
                            variant={
                              isOk
                                ? "default"
                                : isUnconfigured
                                  ? "outline"
                                  : "destructive"
                            }
                            className={`mt-0.5 text-xs ${
                              isOk
                                ? "bg-emerald-500/15 text-emerald-400 hover:bg-emerald-500/20 border-0"
                                : isUnconfigured
                                  ? "text-muted-foreground"
                                  : ""
                            }`}
                          >
                            {isOk ? "정상" : isUnconfigured ? "미설정" : "오류"}
                          </Badge>
                          {p.error && (
                            <p
                              className="mt-1 text-[10px] text-destructive truncate"
                              title={p.error}
                            >
                              {p.error.length > 40
                                ? p.error.slice(0, 40) + "…"
                                : p.error}
                            </p>
                          )}
                          {p.checkedAt && (
                            <p className="mt-0.5 text-[10px] text-muted-foreground">
                              {new Date(p.checkedAt).toLocaleString("ko-KR", {
                                month: "2-digit",
                                day: "2-digit",
                                hour: "2-digit",
                                minute: "2-digit",
                              })}
                            </p>
                          )}
                        </>
                      )}
                    </div>
                  </CardContent>
                </Card>
              );
            })}
          </div>
        </section>
      )}

      {/* ================================================================= */}
      {/* Stats Section (period-aware)                                      */}
      {/* ================================================================= */}
      <section>
        {/* Section header with period filter */}
        <div className="mb-4">
          <h2 className="text-sm font-semibold text-muted-foreground uppercase tracking-wide mb-2">
            통계
          </h2>
          <div className="inline-flex items-center rounded-lg bg-muted p-1 gap-0.5">
            {PERIODS.map((p) => (
              <button
                key={p.value}
                onClick={() => handlePeriodChange(p.value)}
                className={`px-3 py-1 text-xs font-medium rounded-md transition-all ${
                  period === p.value
                    ? "bg-background text-foreground shadow-sm"
                    : "text-muted-foreground hover:text-foreground"
                }`}
              >
                {p.label}
              </button>
            ))}
          </div>
        </div>

        {/* Summary stat cards */}
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Card className="border-border/50">
            <CardContent className="flex items-center gap-3 p-4">
              <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/15">
                <MessageSquare className="h-4 w-4 text-primary" />
              </div>
              <div>
                <p className="text-xs text-muted-foreground">총 메시지</p>
                {loadingUsage ? (
                  <div className="mt-1 h-5 w-16 animate-pulse rounded bg-muted" />
                ) : (
                  <p className="text-lg font-bold text-foreground">
                    {usage?.totalMessages.toLocaleString() ?? "—"}
                  </p>
                )}
              </div>
            </CardContent>
          </Card>

          <Card className="border-border/50">
            <CardContent className="flex items-center gap-3 p-4">
              <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/15">
                <Bot className="h-4 w-4 text-primary" />
              </div>
              <div>
                <p className="text-xs text-muted-foreground">봇 응답</p>
                {loadingUsage ? (
                  <div className="mt-1 h-5 w-16 animate-pulse rounded bg-muted" />
                ) : (
                  <p className="text-lg font-bold text-foreground">
                    {usage?.botMessages.toLocaleString() ?? "—"}
                  </p>
                )}
              </div>
            </CardContent>
          </Card>

          <Card className="border-border/50">
            <CardContent className="flex items-center gap-3 p-4">
              <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/15">
                <Coins className="h-4 w-4 text-primary" />
              </div>
              <div className="min-w-0 flex-1">
                <p className="text-xs text-muted-foreground">총 토큰</p>
                {loadingUsage ? (
                  <div className="mt-1 h-5 w-16 animate-pulse rounded bg-muted" />
                ) : (
                  <>
                    <p className="text-lg font-bold text-foreground">
                      {usage ? formatTokens(usage.totalTokens) : "—"}
                    </p>
                    {usage?.tokenBreakdown && usage.totalTokens > 0 && (
                      <div className="mt-0.5 flex flex-wrap gap-x-2 text-[10px] text-muted-foreground">
                        <span>IN <strong className="text-foreground">{formatTokens(usage.tokenBreakdown.input)}</strong></span>
                        <span>OUT <strong className="text-foreground">{formatTokens(usage.tokenBreakdown.output)}</strong></span>
                        <span>캐시 <strong className="text-foreground">{formatTokens(usage.tokenBreakdown.cacheRead)}</strong></span>
                      </div>
                    )}
                  </>
                )}
              </div>
            </CardContent>
          </Card>

          <Card className="border-border/50">
            <CardContent className="flex items-center gap-3 p-4">
              <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/15">
                <DollarSign className="h-4 w-4 text-primary" />
              </div>
              <div>
                <p className="text-xs text-muted-foreground">총 비용 (USD)</p>
                {loadingUsage ? (
                  <div className="mt-1 h-5 w-16 animate-pulse rounded bg-muted" />
                ) : (
                  <p className="text-lg font-bold text-foreground">
                    {usage ? formatCost(usage.totalCostUsd) : "—"}
                  </p>
                )}
              </div>
            </CardContent>
          </Card>
        </div>

        {/* Charts */}
        <div className="mt-4 grid grid-cols-1 gap-4 lg:grid-cols-2">
          {/* Top bots list */}
          <Card className="border-border/50">
            <CardHeader className="pb-2">
              <div className="flex items-center justify-between">
                <CardTitle className="text-sm font-semibold">상위 봇</CardTitle>
                <PeriodToggle
                  value={botsPeriod}
                  globalValue={period}
                  onChange={(p) => {
                    setBotsPeriod(p);
                    if (p !== null) fetchBotsData(p);
                  }}
                />
              </div>
            </CardHeader>
            <CardContent className="space-y-3">
              {isBotsLoading ? (
                <div className="space-y-3 py-4">
                  {Array.from({ length: 3 }).map((_, i) => (
                    <div key={i} className="flex items-center justify-between">
                      <div className="h-4 w-28 animate-pulse rounded bg-muted" />
                      <div className="h-4 w-20 animate-pulse rounded bg-muted" />
                    </div>
                  ))}
                </div>
              ) : !effectiveBotData.length ? (
                <p className="text-sm text-muted-foreground text-center py-8">데이터가 없습니다</p>
              ) : (
                effectiveBotData.slice(0, 3).map((bot, i) => (
                  <div key={bot.botId} className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <span className="text-xs font-mono text-muted-foreground w-4">{i + 1}</span>
                      <span className="text-sm font-medium truncate">{bot.botName}</span>
                    </div>
                    <div className="flex gap-3 text-xs text-muted-foreground">
                      <span>받은 <strong className="text-foreground">{bot.received.toLocaleString()}</strong></span>
                      <span>보낸 <strong className="text-foreground">{bot.sent.toLocaleString()}</strong></span>
                    </div>
                  </div>
                ))
              )}
            </CardContent>
          </Card>

          {/* Area chart: daily trend */}
          <Card className="border-border/50">
            <CardHeader className="pb-2">
              <div className="flex items-center justify-between">
                <CardTitle className="text-sm font-semibold">
                  일별 활동 추이
                </CardTitle>
                <PeriodToggle
                  value={chartPeriod}
                  globalValue={period}
                  onChange={(p) => {
                    setChartPeriod(p);
                    if (p !== null) fetchChartData(p);
                  }}
                />
              </div>
            </CardHeader>
            <CardContent className="px-2 pb-4">
              {isChartLoading ? (
                <ChartSkeleton />
              ) : effectiveDailyData.length === 0 ? (
                <EmptyChart message="데이터가 없습니다" />
              ) : (
                <ResponsiveContainer width="100%" height={300}>
                  <AreaChart
                    data={effectiveDailyData}
                    margin={{ top: 5, right: 30, left: 10, bottom: 5 }}
                  >
                    <defs>
                      <linearGradient
                        id="colorMessages"
                        x1="0"
                        y1="0"
                        x2="0"
                        y2="1"
                      >
                        <stop
                          offset="5%"
                          stopColor={CHART_COLORS.primary}
                          stopOpacity={0.3}
                        />
                        <stop
                          offset="95%"
                          stopColor={CHART_COLORS.primary}
                          stopOpacity={0}
                        />
                      </linearGradient>
                      <linearGradient
                        id="colorBotMessages"
                        x1="0"
                        y1="0"
                        x2="0"
                        y2="1"
                      >
                        <stop
                          offset="5%"
                          stopColor={CHART_COLORS.secondary}
                          stopOpacity={0.3}
                        />
                        <stop
                          offset="95%"
                          stopColor={CHART_COLORS.secondary}
                          stopOpacity={0}
                        />
                      </linearGradient>
                    </defs>
                    <CartesianGrid
                      strokeDasharray="3 3"
                      stroke="hsl(var(--border))"
                    />
                    <XAxis
                      dataKey="dateLabel"
                      fontSize={12}
                      tickLine={false}
                    />
                    <YAxis fontSize={12} tickLine={false} axisLine={false} />
                    <Tooltip content={<CustomTooltip />} />
                    <Area
                      type="monotone"
                      dataKey="messages"
                      name="메시지"
                      stroke={CHART_COLORS.primary}
                      fillOpacity={1}
                      fill="url(#colorMessages)"
                      strokeWidth={2}
                    />
                    <Area
                      type="monotone"
                      dataKey="botMessages"
                      name="봇 응답"
                      stroke={CHART_COLORS.secondary}
                      fillOpacity={1}
                      fill="url(#colorBotMessages)"
                      strokeWidth={2}
                    />
                  </AreaChart>
                </ResponsiveContainer>
              )}
            </CardContent>
          </Card>
        </div>
      </section>
    </div>
  );
}
