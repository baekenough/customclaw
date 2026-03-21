import { NextRequest } from "next/server";
import { auth } from "@/lib/auth";
import { prisma } from "@/lib/prisma";

type Period = "day" | "week" | "month" | "all";

const PERIOD_DAYS: Record<Exclude<Period, "all">, number> = {
  day: 1,
  week: 7,
  month: 30,
};

function resolvePeriod(searchParams: URLSearchParams): Period {
  const period = searchParams.get("period") as Period | null;
  if (period && ["day", "week", "month", "all"].includes(period)) {
    return period;
  }

  // Backward compat: convert legacy `days` param to nearest period
  const daysParam = searchParams.get("days");
  if (daysParam) {
    const days = parseInt(daysParam, 10);
    if (days <= 1) return "day";
    if (days <= 7) return "week";
    if (days <= 30) return "month";
    return "all";
  }

  return "month";
}

function getSince(period: Period): Date | null {
  if (period === "all") return null;
  const since = new Date();
  since.setDate(since.getDate() - PERIOD_DAYS[period]);
  return since;
}

// ── Raw SQL helpers ──────────────────────────────────────────────

interface DailyMessageRow {
  date: string;
  messages: string;
  bot_messages: string;
}

interface DailyUsageRow {
  date: string;
  tokens: string;
  cost_usd: string | null;
}

interface BotCallRow {
  bot_id: string;
  received: string;
  sent: string;
}

interface TokenBreakdownRow {
  input_tokens: string;
  output_tokens: string;
  cache_read_tokens: string;
  cache_creation_tokens: string;
}

async function fetchDailyMessages(
  since: Date | null,
  botId: string | null,
): Promise<DailyMessageRow[]> {
  const conditions: string[] = [];
  const params: unknown[] = [];
  let idx = 1;

  if (since) {
    conditions.push(`timestamp >= $${idx}`);
    params.push(since);
    idx++;
  }
  if (botId) {
    conditions.push(`bot_id = $${idx}`);
    params.push(botId);
  }

  const where = conditions.length > 0 ? `WHERE ${conditions.join(" AND ")}` : "";

  return prisma.$queryRawUnsafe<DailyMessageRow[]>(
    `SELECT DATE(timestamp) AS date,
            COUNT(*)::text AS messages,
            COUNT(*) FILTER (WHERE role = 'assistant')::text AS bot_messages
     FROM messages
     ${where}
     GROUP BY DATE(timestamp)
     ORDER BY date`,
    ...params,
  );
}

async function fetchDailyUsage(
  since: Date | null,
  botId: string | null,
): Promise<DailyUsageRow[]> {
  const conditions: string[] = [];
  const params: unknown[] = [];
  let idx = 1;

  if (since) {
    conditions.push(`created_at >= $${idx}`);
    params.push(since);
    idx++;
  }
  if (botId) {
    conditions.push(`bot_id = $${idx}`);
    params.push(botId);
  }

  const where = conditions.length > 0 ? `WHERE ${conditions.join(" AND ")}` : "";

  return prisma.$queryRawUnsafe<DailyUsageRow[]>(
    `SELECT DATE(created_at) AS date,
            SUM(input_tokens + output_tokens)::text AS tokens,
            SUM(cost_usd)::text AS cost_usd
     FROM api_usage_logs
     ${where}
     GROUP BY DATE(created_at)
     ORDER BY date`,
    ...params,
  );
}

async function fetchTokenBreakdown(
  since: Date | null,
  botId: string | null,
): Promise<TokenBreakdownRow[]> {
  const conditions: string[] = [];
  const params: unknown[] = [];
  let idx = 1;

  if (since) {
    conditions.push(`created_at >= $${idx}::timestamptz`);
    params.push(since);
    idx++;
  }
  if (botId) {
    conditions.push(`bot_id = $${idx}`);
    params.push(botId);
  }

  const where = conditions.length > 0 ? `WHERE ${conditions.join(" AND ")}` : "";

  return prisma.$queryRawUnsafe<TokenBreakdownRow[]>(
    `SELECT COALESCE(SUM(input_tokens), 0)::text as input_tokens,
            COALESCE(SUM(output_tokens), 0)::text as output_tokens,
            COALESCE(SUM(cache_read_tokens), 0)::text as cache_read_tokens,
            COALESCE(SUM(cache_creation_tokens), 0)::text as cache_creation_tokens
     FROM api_usage_logs
     ${where}`,
    ...params,
  );
}

async function fetchBotCalls(
  since: Date | null,
  botId: string | null,
): Promise<BotCallRow[]> {
  const conditions: string[] = [];
  const params: unknown[] = [];
  let idx = 1;

  if (since) {
    conditions.push(`timestamp >= $${idx}`);
    params.push(since);
    idx++;
  }
  if (botId) {
    conditions.push(`bot_id = $${idx}`);
    params.push(botId);
  }

  const where = conditions.length > 0 ? `WHERE ${conditions.join(" AND ")}` : "";

  return prisma.$queryRawUnsafe<BotCallRow[]>(
    `SELECT bot_id,
            COUNT(*) FILTER (WHERE role = 'user')::text AS received,
            COUNT(*) FILTER (WHERE role = 'assistant')::text AS sent
     FROM messages
     ${where}
     GROUP BY bot_id
     ORDER BY COUNT(*) DESC`,
    ...params,
  );
}

// ── Main handler ─────────────────────────────────────────────────

export async function GET(request: NextRequest) {
  const session = await auth();
  if (!session) {
    return Response.json({ error: "Unauthorized" }, { status: 401 });
  }

  try {
    const { searchParams } = new URL(request.url);
    const botId = searchParams.get("botId");
    const period = resolvePeriod(searchParams);
    const since = getSince(period);

    // Parallel: bot-level aggregation + summary counts + daily data + bot calls + token breakdown
    const [logs, totalMessages, botMessages, dailyMessages, dailyUsage, botCallRows, tokenBreakdownRows] =
      await Promise.all([
        prisma.apiUsageLog.findMany({
          where: {
            ...(botId ? { botId } : {}),
            ...(since ? { createdAt: { gte: since } } : {}),
          },
          include: {
            bot: { select: { id: true, name: true } },
          },
        }),
        prisma.message.count({
          where: {
            ...(botId ? { botId } : {}),
            ...(since ? { timestamp: { gte: since } } : {}),
          },
        }),
        prisma.message.count({
          where: {
            ...(botId ? { botId } : {}),
            ...(since ? { timestamp: { gte: since } } : {}),
            role: "assistant",
          },
        }),
        fetchDailyMessages(since, botId),
        fetchDailyUsage(since, botId),
        fetchBotCalls(since, botId),
        fetchTokenBreakdown(since, botId),
      ]);

    // ── Aggregate by bot + model ────────────────────────────────
    const agg = new Map<
      string,
      {
        botId: string;
        botName: string;
        model: string;
        inputTokens: number;
        outputTokens: number;
        totalTokens: number;
        costUsd: number;
        calls: number;
      }
    >();

    for (const log of logs) {
      const key = `${log.botId}::${log.model}`;
      const existing = agg.get(key);
      const cost = log.costUsd ? Number(log.costUsd) : 0;
      if (existing) {
        existing.inputTokens += log.inputTokens;
        existing.outputTokens += log.outputTokens;
        existing.totalTokens += log.inputTokens + log.outputTokens;
        existing.costUsd += cost;
        existing.calls += 1;
      } else {
        agg.set(key, {
          botId: log.botId,
          botName: log.bot.name,
          model: log.model,
          inputTokens: log.inputTokens,
          outputTokens: log.outputTokens,
          totalTokens: log.inputTokens + log.outputTokens,
          costUsd: cost,
          calls: 1,
        });
      }
    }

    const stats = Array.from(agg.values());

    // ── Merge daily message counts + usage into unified daily array
    const dailyMap = new Map<
      string,
      { date: string; messages: number; botMessages: number; tokens: number; costUsd: number }
    >();

    for (const row of dailyMessages) {
      const dateStr = String(row.date);
      dailyMap.set(dateStr, {
        date: dateStr,
        messages: parseInt(row.messages, 10),
        botMessages: parseInt(row.bot_messages, 10),
        tokens: 0,
        costUsd: 0,
      });
    }

    for (const row of dailyUsage) {
      const dateStr = String(row.date);
      const existing = dailyMap.get(dateStr);
      const tokens = parseInt(row.tokens, 10) || 0;
      const costUsd = parseFloat(row.cost_usd ?? "0") || 0;
      if (existing) {
        existing.tokens = tokens;
        existing.costUsd = costUsd;
      } else {
        dailyMap.set(dateStr, {
          date: dateStr,
          messages: 0,
          botMessages: 0,
          tokens,
          costUsd,
        });
      }
    }

    const daily = Array.from(dailyMap.values()).sort((a, b) =>
      a.date.localeCompare(b.date),
    );

    // ── Build botCalls with names from existing bot data ────────
    const botNameMap = new Map<string, string>();
    for (const log of logs) {
      if (!botNameMap.has(log.botId)) {
        botNameMap.set(log.botId, log.bot.name);
      }
    }

    // Fetch any bot names not already loaded via usage logs
    const missingBotIds = botCallRows
      .map((r) => r.bot_id)
      .filter((id) => !botNameMap.has(id));

    if (missingBotIds.length > 0) {
      const bots = await prisma.bot.findMany({
        where: { id: { in: missingBotIds } },
        select: { id: true, name: true },
      });
      for (const bot of bots) {
        botNameMap.set(bot.id, bot.name);
      }
    }

    const botCalls = botCallRows.map((row) => ({
      botId: row.bot_id,
      botName: botNameMap.get(row.bot_id) ?? row.bot_id,
      received: parseInt(row.received) || 0,
      sent: parseInt(row.sent) || 0,
    }));

    return Response.json({
      totalMessages,
      botMessages,
      totalCostUsd: stats.reduce((sum, s) => sum + s.costUsd, 0),
      totalTokens: stats.reduce((sum, s) => sum + s.totalTokens, 0),
      tokenBreakdown: {
        input: parseInt(tokenBreakdownRows[0]?.input_tokens) || 0,
        output: parseInt(tokenBreakdownRows[0]?.output_tokens) || 0,
        cacheRead: parseInt(tokenBreakdownRows[0]?.cache_read_tokens) || 0,
        cacheCreation: parseInt(tokenBreakdownRows[0]?.cache_creation_tokens) || 0,
      },
      stats,
      daily,
      botCalls,
    });
  } catch (error) {
    console.error("GET /api/usage error:", error);
    return Response.json({ error: "Internal server error" }, { status: 500 });
  }
}
