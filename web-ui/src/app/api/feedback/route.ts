import { auth } from "@/lib/auth";

export async function POST(request: Request) {
  const session = await auth();
  if (!session) {
    return Response.json({ error: "Unauthorized" }, { status: 401 });
  }

  try {
    const { type, content } = await request.json();

    if (!content?.trim()) {
      return Response.json({ error: "Content required" }, { status: 400 });
    }

    // Create GitHub issue via GitHub API
    const token = process.env.GITHUB_TOKEN;
    const repo = "baekenough/customclaw";

    if (!token) {
      console.error("GITHUB_TOKEN not set, feedback not submitted");
      return Response.json({ ok: true }); // Don't reveal to user
    }

    const labelMap: Record<string, string> = {
      bug: "bug",
      feature: "enhancement",
      improvement: "improvement",
    };

    const title = `[Feedback/${type}] ${content.trim().slice(0, 60)}`;
    const body = [
      `## Feedback`,
      ``,
      `**Type:** ${type}`,
      `**From:** ${session.user?.name ?? "anonymous"} (${session.user?.email ?? ""})`,
      `**Date:** ${new Date().toISOString()}`,
      ``,
      `### Content`,
      ``,
      content.trim(),
      ``,
      `---`,
      `_Submitted via customclaw Web UI feedback form._`,
    ].join("\n");

    await fetch(`https://api.github.com/repos/${repo}/issues`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
        Accept: "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
      },
      body: JSON.stringify({
        title,
        body,
        labels: ["feedback", labelMap[type] ?? "improvement"],
      }),
    });

    return Response.json({ ok: true });
  } catch (error) {
    console.error("POST /api/feedback error:", error);
    return Response.json({ error: "Internal server error" }, { status: 500 });
  }
}
