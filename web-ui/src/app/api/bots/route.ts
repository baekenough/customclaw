import { NextRequest } from "next/server";
import { auth } from "@/lib/auth";
import { prisma } from "@/lib/prisma";

function maskApiKey(key: string | null): string | null {
  if (!key || key.length < 8) return key ? "••••" : null;
  return key.slice(0, 4) + "••••" + key.slice(-4);
}

export async function GET() {
  const session = await auth();
  if (!session) {
    return Response.json({ error: "Unauthorized" }, { status: 401 });
  }

  try {
    const bots = await prisma.bot.findMany({
      orderBy: { createdAt: "desc" },
    });
    const maskedBots = bots.map((bot: typeof bots[number]) => ({
      ...bot,
      anthropicApiKey: maskApiKey(bot.anthropicApiKey),
      openaiApiKey: maskApiKey(bot.openaiApiKey),
      geminiApiKey: maskApiKey(bot.geminiApiKey),
    }));
    return Response.json(maskedBots);
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
      credentials,
      channels,
      persona,
      project,
      airflow,
      tools,
      claude,
      memory,
      security,
      isActive,
      anthropicApiKey,
      openaiApiKey,
      geminiApiKey,
    } = body;

    if (!id || !name) {
      return Response.json({ error: "id and name are required" }, { status: 400 });
    }
    if (!platform) {
      return Response.json({ error: "platform is required" }, { status: 400 });
    }

    // Support both legacy fields and new credentials object
    if (platform === "slack") {
      const appToken = slackAppToken || credentials?.app_token;
      const botToken = slackBotToken || credentials?.bot_token;
      if (!appToken || !botToken) {
        return Response.json(
          { error: "Slack requires app_token and bot_token" },
          { status: 400 }
        );
      }
    }
    if (platform === "discord") {
      const token = discord?.token || credentials?.token;
      if (!token) {
        return Response.json(
          { error: "Discord requires token" },
          { status: 400 }
        );
      }
    }
    if (platform === "mattermost") {
      const url = mattermost?.url || credentials?.url;
      const token = mattermost?.token || credentials?.token;
      if (!url || !token) {
        return Response.json(
          { error: "Mattermost requires url and token" },
          { status: 400 }
        );
      }
    }

    // Resolve legacy fields from credentials for backward compat
    const resolvedSlackAppToken = slackAppToken || credentials?.app_token || "";
    const resolvedSlackBotToken = slackBotToken || credentials?.bot_token || "";

    const bot = await prisma.bot.create({
      data: {
        id,
        name,
        platform,
        slackAppToken: resolvedSlackAppToken,
        slackBotToken: resolvedSlackBotToken,
        ...(discord !== undefined && { discord }),
        ...(mattermost !== undefined && { mattermost }),
        credentials: credentials ?? {},
        channels: channels ?? [],
        persona: persona ?? {},
        project: project ?? {},
        airflow: airflow ?? {},
        tools: tools ?? {},
        claude: claude ?? {},
        memory: memory ?? {},
        security: security ?? {},
        isActive: isActive ?? true,
        ...(anthropicApiKey !== undefined && { anthropicApiKey }),
        ...(openaiApiKey !== undefined && { openaiApiKey }),
        ...(geminiApiKey !== undefined && { geminiApiKey }),
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
