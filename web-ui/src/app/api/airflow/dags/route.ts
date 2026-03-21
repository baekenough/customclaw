import { auth } from "@/lib/auth";
import { airflowFetch } from "@/lib/airflow";

export async function GET() {
  const session = await auth();
  if (!session) {
    return Response.json({ error: "Unauthorized" }, { status: 401 });
  }

  try {
    const res = await airflowFetch("/dags");
    if (!res.ok) {
      return Response.json(
        { error: `Airflow error: ${res.status}` },
        { status: res.status }
      );
    }
    const data = await res.json();
    return Response.json(data);
  } catch (error) {
    console.error("GET /api/airflow/dags error:", error);
    return Response.json({ error: "Failed to reach Airflow" }, { status: 503 });
  }
}
