import { NextRequest } from "next/server";
import { auth } from "@/lib/auth";
import { prisma } from "@/lib/prisma";

export async function GET(request: NextRequest) {
  const session = await auth();
  if (!session) {
    return Response.json({ error: "Unauthorized" }, { status: 401 });
  }

  try {
    const { searchParams } = new URL(request.url);
    const botId = searchParams.get("botId");
    const limit = Math.min(parseInt(searchParams.get("limit") ?? "50"), 200);

    const messages = await prisma.message.findMany({
      where: botId ? { botId } : undefined,
      orderBy: { timestamp: "desc" },
      take: limit,
      include: {
        bot: { select: { id: true, name: true } },
      },
    });

    return Response.json(messages);
  } catch (error) {
    console.error("GET /api/messages error:", error);
    return Response.json({ error: "Internal server error" }, { status: 500 });
  }
}
