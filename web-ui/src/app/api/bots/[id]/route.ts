import { NextRequest } from "next/server";
import { auth } from "@/lib/auth";
import { prisma } from "@/lib/prisma";

function maskApiKey(key: string | null): string | null {
  if (!key || key.length < 8) return key ? "••••" : null;
  return key.slice(0, 4) + "••••" + key.slice(-4);
}

export async function GET(
  _request: NextRequest,
  { params }: { params: Promise<{ id: string }> }
) {
  const session = await auth();
  if (!session) {
    return Response.json({ error: "Unauthorized" }, { status: 401 });
  }

  try {
    const { id } = await params;
    const bot = await prisma.bot.findUnique({ where: { id } });
    if (!bot) {
      return Response.json({ error: "Bot not found" }, { status: 404 });
    }
    return Response.json({
      ...bot,
      anthropicApiKey: maskApiKey(bot.anthropicApiKey),
      openaiApiKey: maskApiKey(bot.openaiApiKey),
      geminiApiKey: maskApiKey(bot.geminiApiKey),
    });
  } catch (error) {
    console.error("GET /api/bots/[id] error:", error);
    return Response.json({ error: "Internal server error" }, { status: 500 });
  }
}

export async function PUT(
  request: NextRequest,
  { params }: { params: Promise<{ id: string }> }
) {
  const session = await auth();
  if (!session) {
    return Response.json({ error: "Unauthorized" }, { status: 401 });
  }

  try {
    const { id } = await params;
    const body = await request.json();

    const existing = await prisma.bot.findUnique({ where: { id } });
    if (!existing) {
      return Response.json({ error: "Bot not found" }, { status: 404 });
    }

    const {
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

    // Validate platform credentials if platform is being updated
    const effectivePlatform = platform ?? existing.platform;
    if (effectivePlatform === "slack") {
      const appToken = slackAppToken || credentials?.app_token;
      const botToken = slackBotToken || credentials?.bot_token;
      // Only enforce if tokens are explicitly being cleared (empty string)
      if (slackAppToken === "" && !credentials?.app_token) {
        return Response.json(
          { error: "Slack requires app_token and bot_token" },
          { status: 400 }
        );
      }
      if (slackBotToken === "" && !credentials?.bot_token) {
        return Response.json(
          { error: "Slack requires app_token and bot_token" },
          { status: 400 }
        );
      }
      void appToken;
      void botToken;
    }

    // Resolve legacy fields from credentials for backward compat
    const resolvedSlackAppToken =
      slackAppToken !== undefined
        ? slackAppToken || credentials?.app_token || ""
        : undefined;
    const resolvedSlackBotToken =
      slackBotToken !== undefined
        ? slackBotToken || credentials?.bot_token || ""
        : undefined;

    const bot = await prisma.bot.update({
      where: { id },
      data: {
        ...(name !== undefined && { name }),
        ...(platform !== undefined && { platform }),
        ...(resolvedSlackAppToken !== undefined && { slackAppToken: resolvedSlackAppToken }),
        ...(resolvedSlackBotToken !== undefined && { slackBotToken: resolvedSlackBotToken }),
        ...(discord !== undefined && { discord }),
        ...(mattermost !== undefined && { mattermost }),
        ...(credentials !== undefined && { credentials }),
        ...(channels !== undefined && { channels }),
        ...(persona !== undefined && { persona }),
        ...(project !== undefined && { project }),
        ...(airflow !== undefined && { airflow }),
        ...(tools !== undefined && { tools }),
        ...(claude !== undefined && { claude }),
        ...(memory !== undefined && { memory }),
        ...(security !== undefined && { security }),
        ...(isActive !== undefined && { isActive }),
        ...(anthropicApiKey !== undefined && { anthropicApiKey: anthropicApiKey || null }),
        ...(openaiApiKey !== undefined && { openaiApiKey: openaiApiKey || null }),
        ...(geminiApiKey !== undefined && { geminiApiKey: geminiApiKey || null }),
      },
    });

    return Response.json(bot);
  } catch (error) {
    console.error("PUT /api/bots/[id] error:", error);
    return Response.json({ error: "Internal server error" }, { status: 500 });
  }
}

export async function DELETE(
  _request: NextRequest,
  { params }: { params: Promise<{ id: string }> }
) {
  const session = await auth();
  if (!session) {
    return Response.json({ error: "Unauthorized" }, { status: 401 });
  }

  try {
    const { id } = await params;
    const existing = await prisma.bot.findUnique({ where: { id } });
    if (!existing) {
      return Response.json({ error: "Bot not found" }, { status: 404 });
    }

    await prisma.bot.delete({ where: { id } });
    return Response.json({ success: true });
  } catch (error) {
    console.error("DELETE /api/bots/[id] error:", error);
    return Response.json({ error: "Internal server error" }, { status: 500 });
  }
}
