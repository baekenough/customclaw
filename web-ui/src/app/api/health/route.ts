import { auth } from "@/lib/auth";
import { prisma } from "@/lib/prisma";
import { checkAirflowHealth } from "@/lib/airflow";
import * as net from "net";

async function checkRedis(): Promise<boolean> {
  return new Promise((resolve) => {
    const socket = net.createConnection({ host: "redis", port: 6379 });
    socket.setTimeout(3000);
    socket.on("connect", () => {
      socket.destroy();
      resolve(true);
    });
    socket.on("error", () => resolve(false));
    socket.on("timeout", () => {
      socket.destroy();
      resolve(false);
    });
  });
}

export async function GET() {
  const session = await auth();
  if (!session) {
    return Response.json({ error: "Unauthorized" }, { status: 401 });
  }

  const [postgresResult, redisResult, opensearchResult, airflowResult] =
    await Promise.allSettled([
      // PostgreSQL
      prisma.$queryRaw`SELECT 1`.then(() => true),
      // Redis
      checkRedis(),
      // OpenSearch
      fetch("http://opensearch:9200/_cluster/health", {
        cache: "no-store",
        signal: AbortSignal.timeout(3000),
      }).then((r) => r.ok),
      // Airflow — checks component-level health status
      checkAirflowHealth(),
    ]);

  const postgres =
    postgresResult.status === "fulfilled" ? postgresResult.value : false;
  const redis =
    redisResult.status === "fulfilled" ? redisResult.value : false;
  const opensearch =
    opensearchResult.status === "fulfilled" ? opensearchResult.value : false;
  const airflow =
    airflowResult.status === "fulfilled"
      ? airflowResult.value
      : { status: false };

  const allHealthy =
    postgres === true &&
    redis === true &&
    opensearch === true &&
    airflow.status === true;

  // LLM 크레덴셜 상태 조회 (테이블 미존재 시 무시)
  let llmProviders: Array<{
    provider: string;
    status: string;
    error: string | null;
    errorKind: string | null;
    checkedAt: string;
  }> = [];
  try {
    const rows = await prisma.credentialStatus.findMany();
    llmProviders = rows.map((r) => ({
      provider: r.provider,
      status: r.status,
      error: r.error,
      errorKind: r.errorKind ?? null,
      checkedAt: r.checkedAt.toISOString(),
    }));
  } catch {
    // Table may not exist yet — ignore
  }

  return Response.json(
    {
      postgres,
      redis,
      opensearch,
      airflow,
      healthy: allHealthy,
      llmProviders,
    },
    { status: allHealthy ? 200 : 503 }
  );
}
