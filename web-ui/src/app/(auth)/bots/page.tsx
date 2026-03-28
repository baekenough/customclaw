"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Plus, Pencil, Trash2, Bot } from "lucide-react";
import { buttonVariants } from "@/components/ui/button";

interface BotData {
  id: string;
  name: string;
  platform?: string;
  isActive: boolean;
  channels: string[];
  security?: { allowed_channels?: string[] };
  claude: { provider?: string; model?: string };
  createdAt: string;
}

type ProviderKey = "claude" | "claude-cli" | "codex" | "openai" | string;

function getProviderLabel(p: ProviderKey): string {
  if (p === "claude" || p === "claude-cli") return "Claude";
  if (p === "codex") return "Codex";
  if (p === "openai") return "OpenAI";
  return p || "—";
}

function getProviderBadgeClass(p: ProviderKey): string {
  if (p === "claude" || p === "claude-cli")
    return "bg-violet-500/15 text-violet-400 border-0";
  if (p === "codex" || p === "openai")
    return "bg-emerald-500/15 text-emerald-400 border-0";
  return "";
}

function getChannelCount(bot: BotData): number {
  // Discord bots: check discord-specific config for guild/channel info
  if (bot.platform === "discord") {
    const discord = (bot as unknown as Record<string, unknown>).discord as Record<string, unknown> | undefined;
    if (discord?.guild_id) return 1; // Connected to 1 guild
    return 0;
  }
  // Slack bots: existing logic
  if (bot.channels?.length > 0) return bot.channels.length;
  if ((bot.security?.allowed_channels?.length ?? 0) > 0)
    return bot.security!.allowed_channels!.length;
  return 0;
}

export default function BotsPage() {
  const router = useRouter();
  const [bots, setBots] = useState<BotData[]>([]);
  const [loading, setLoading] = useState(true);
  const [deleteTarget, setDeleteTarget] = useState<BotData | null>(null);
  const [deleting, setDeleting] = useState(false);

  const fetchBots = () => {
    setLoading(true);
    fetch("/api/bots")
      .then((r) => r.json())
      .then(setBots)
      .catch(() => setBots([]))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    fetchBots();
  }, []);

  const handleDelete = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      const res = await fetch(`/api/bots/${deleteTarget.id}`, {
        method: "DELETE",
      });
      if (res.ok) {
        setBots((prev) => prev.filter((b) => b.id !== deleteTarget.id));
        setDeleteTarget(null);
      }
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">봇 관리</h1>
          <p className="text-sm text-muted-foreground mt-1">
            등록된 봇 목록
          </p>
        </div>
        <Link
          href="/bots/new"
          className={buttonVariants({ variant: "default", className: "gap-2" })}
        >
          <Plus className="h-4 w-4" />새 봇 만들기
        </Link>
      </div>

      <Card className="border-border/50">
        <CardHeader className="pb-3">
          <CardTitle className="text-base">봇 목록</CardTitle>
          <CardDescription className="text-xs">
            총 {bots.length}개의 봇
          </CardDescription>
        </CardHeader>
        <CardContent className="px-0">
          {loading ? (
            <div className="space-y-3 px-6 pb-4">
              {[1, 2, 3].map((i) => (
                <div
                  key={i}
                  className="h-12 w-full animate-pulse rounded-lg bg-muted"
                />
              ))}
            </div>
          ) : bots.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center px-6">
              <Bot className="h-12 w-12 text-muted-foreground/30 mb-4" />
              <p className="text-sm font-medium text-muted-foreground">
                등록된 봇이 없습니다
              </p>
              <p className="text-xs text-muted-foreground/60 mt-1">
                새 봇 만들기 버튼으로 시작하세요
              </p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow className="border-border/50 hover:bg-transparent">
                  <TableHead className="text-xs">이름</TableHead>
                  <TableHead className="text-xs">플랫폼</TableHead>
                  <TableHead className="text-xs">Provider</TableHead>
                  <TableHead className="text-xs">모델</TableHead>
                  <TableHead className="text-xs">상태</TableHead>
                  <TableHead className="text-xs">채널</TableHead>
                  <TableHead className="text-xs text-right">작업</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {bots.map((bot) => (
                  <TableRow
                    key={bot.id}
                    className="border-border/30 hover:bg-muted/20"
                  >
                    <TableCell>
                      <div>
                        <p className="text-sm font-medium">{bot.name}</p>
                        <p className="text-xs text-muted-foreground">
                          {bot.id}
                        </p>
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant={
                          bot.platform === "discord"
                            ? "secondary"
                            : bot.platform === "mattermost"
                            ? "outline"
                            : "default"
                        }
                        className={`text-xs capitalize ${
                          bot.platform === "discord"
                            ? "bg-violet-500/15 text-violet-400 border-0 hover:bg-violet-500/20"
                            : bot.platform === "slack"
                            ? "bg-emerald-500/15 text-emerald-400 border-0 hover:bg-emerald-500/20"
                            : ""
                        }`}
                      >
                        {bot.platform ?? "slack"}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <div className="flex gap-1">
                        <Badge
                          variant="outline"
                          className={`text-xs ${getProviderBadgeClass(bot.claude?.provider ?? "")}`}
                        >
                          {getProviderLabel(bot.claude?.provider ?? "")}
                        </Badge>
                        {bot.claude?.provider === "claude-cli" && (
                          <Badge className="text-xs bg-amber-500/15 text-amber-400 border-0">
                            CLI
                          </Badge>
                        )}
                        {bot.claude?.provider === "claude" && (
                          <Badge className="text-xs bg-blue-500/15 text-blue-400 border-0">
                            API
                          </Badge>
                        )}
                      </div>
                    </TableCell>
                    <TableCell>
                      <span className="text-sm text-muted-foreground">
                        {bot.claude?.model ?? "—"}
                      </span>
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant={bot.isActive ? "default" : "secondary"}
                        className={`text-xs ${
                          bot.isActive
                            ? "bg-emerald-500/15 text-emerald-400 border-0 hover:bg-emerald-500/20"
                            : ""
                        }`}
                      >
                        {bot.isActive ? "활성" : "비활성"}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <span className="text-sm text-muted-foreground">
                        {getChannelCount(bot)}개
                      </span>
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-8 w-8"
                          onClick={() => router.push(`/bots/${bot.id}`)}
                          aria-label={`${bot.name} 편집`}
                        >
                          <Pencil className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-8 w-8 text-destructive hover:text-destructive"
                          onClick={() => setDeleteTarget(bot)}
                          aria-label={`${bot.name} 삭제`}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {/* Delete Confirmation Dialog */}
      <Dialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>봇 삭제</DialogTitle>
            <DialogDescription>
              <strong className="text-foreground">{deleteTarget?.name}</strong>{" "}
              봇을 삭제하시겠습니까? 이 작업은 되돌릴 수 없으며 모든 관련
              데이터가 삭제됩니다.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setDeleteTarget(null)}
              disabled={deleting}
            >
              취소
            </Button>
            <Button
              variant="destructive"
              onClick={handleDelete}
              disabled={deleting}
            >
              {deleting ? "삭제 중..." : "삭제"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
