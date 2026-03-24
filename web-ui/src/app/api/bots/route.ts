import { NextRequest } from "next/server";
import { auth } from "@/lib/auth";
import { prisma } from "@/lib/prisma";

export async function GET() {
  const session = await auth();
  if (!session) {
    return Response.json({ error: "Unauthorized" }, { status: 401 });
  }

  try {
    const bots = await prisma.bot.findMany({
      orderBy: { createdAt: "desc" },
    });
    return Response.json(bots);
  } catch (error) {
    console.error("GET /api/bots error:", error);
    return Response.json({ error: "Internal server error" }, { status: 500 });
  }
}

export async function POST(request: NextRequest) {
  const session = await auth();
  if (!session) {
    return Response.json({ error: "Unauthorized" }, { status: 401 });
  }

  try {
    const body = await request.json();
    const {
      id,
      name,
      platform,
      slackAppToken,
      slackBotToken,
      discord,
      mattermost,
      channels,
      persona,
      project,
      airflow,
      tools,
      claude,
      memory,
      security,
      isActive,
    } = body;

    const resolvedPlatform = platform ?? "slack";

    if (!id || !name) {
      return Response.json({ error: "id and name are required" }, { status: 400 });
    }
    if (resolvedPlatform === "slack" && (!slackAppToken || !slackBotToken)) {
      return Response.json(
        { error: "slackAppToken and slackBotToken are required for Slack bots" },
        { status: 400 }
      );
    }
    if (resolvedPlatform === "discord" && !discord?.token) {
      return Response.json(
        { error: "discord.token is required for Discord bots" },
        { status: 400 }
      );
    }
    if (resolvedPlatform === "mattermost" && (!mattermost?.url || !mattermost?.token)) {
      return Response.json(
        { error: "mattermost.url and mattermost.token are required for Mattermost bots" },
        { status: 400 }
      );
    }

    const bot = await prisma.bot.create({
      data: {
        id,
        name,
        platform: resolvedPlatform,
        slackAppToken: slackAppToken ?? "",
        slackBotToken: slackBotToken ?? "",
        ...(discord !== undefined && { discord }),
        ...(mattermost !== undefined && { mattermost }),
        channels: channels ?? [],
        persona: persona ?? {},
        project: project ?? {},
        airflow: airflow ?? {},
        tools: tools ?? {},
        claude: claude ?? {},
        memory: memory ?? {},
        security: security ?? {},
        isActive: isActive ?? true,
      },
    });

    return Response.json(bot, { status: 201 });
  } catch (error: unknown) {
    console.error("POST /api/bots error:", error);
    if (
      error instanceof Error &&
      error.message.includes("Unique constraint")
    ) {
      return Response.json({ error: "Bot ID already exists" }, { status: 409 });
    }
    return Response.json({ error: "Internal server error" }, { status: 500 });
  }
}
