import { NextRequest } from "next/server";
import { auth } from "@/lib/auth";
import { prisma } from "@/lib/prisma";

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
    return Response.json(bot);
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

    const bot = await prisma.bot.update({
      where: { id },
      data: {
        ...(name !== undefined && { name }),
        ...(platform !== undefined && { platform }),
        ...(slackAppToken !== undefined && { slackAppToken }),
        ...(slackBotToken !== undefined && { slackBotToken }),
        ...(discord !== undefined && { discord }),
        ...(mattermost !== undefined && { mattermost }),
        ...(channels !== undefined && { channels }),
        ...(persona !== undefined && { persona }),
        ...(project !== undefined && { project }),
        ...(airflow !== undefined && { airflow }),
        ...(tools !== undefined && { tools }),
        ...(claude !== undefined && { claude }),
        ...(memory !== undefined && { memory }),
        ...(security !== undefined && { security }),
        ...(isActive !== undefined && { isActive }),
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
