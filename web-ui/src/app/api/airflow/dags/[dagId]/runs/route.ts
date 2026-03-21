import { NextRequest } from "next/server";
import { auth } from "@/lib/auth";
import { airflowFetch } from "@/lib/airflow";

export async function GET(
  _request: NextRequest,
  { params }: { params: Promise<{ dagId: string }> }
) {
  const session = await auth();
  if (!session) {
    return Response.json({ error: "Unauthorized" }, { status: 401 });
  }

  try {
    const { dagId } = await params;
    const res = await airflowFetch(
      `/dags/${dagId}/dagRuns?limit=20&order_by=-start_date`
    );
    if (!res.ok) {
      return Response.json(
        { error: `Airflow error: ${res.status}` },
        { status: res.status }
      );
    }
    const data = await res.json();
    return Response.json(data);
  } catch (error) {
    console.error("GET /api/airflow/dags/[dagId]/runs error:", error);
    return Response.json({ error: "Failed to reach Airflow" }, { status: 503 });
  }
}
