"use client";

import { useState, useEffect, use } from "react";
import { useRouter } from "next/navigation";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ChevronLeft, Eye, EyeOff, X, Plus, Trash2, HelpCircle } from "lucide-react";
import Link from "next/link";
import { buttonVariants } from "@/components/ui/button";
import { AVAILABLE_TOOLS, DANGEROUS_TOOLS } from "@/lib/bot-tools";

interface BotRaw {
  id: string;
  name: string;
  platform?: string;
  slackAppToken: string;
  slackBotToken: string;
  discord?: { token?: string; guild_id?: string };
  mattermost?: { url?: string; token?: string; port?: number };
  channels: string[];
  isActive: boolean;
  persona: { display_name?: string; description?: string; personality?: string };
  project: { repo_path?: string; github_repo?: string };
  claude: { provider?: string; model?: string; max_turns?: number; full_agent?: boolean };
  memory: { context_window?: number; auto_extract?: boolean };
  tools: { enabled?: string[] };
  security: { allowed_channels?: string[]; dangerous_tools?: string[] };
}

function maskToken(token: string) {
  if (!token || token.length < 8) return token;
  return token.slice(0, 6) + "****" + token.slice(-4);
}

export default function BotDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const [bot, setBot] = useState<BotRaw | null>(null);
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [showAppToken, setShowAppToken] = useState(false);
  const [showBotToken, setShowBotToken] = useState(false);
  const [showDeleteDialog, setShowDeleteDialog] = useState(false);
  const [deleting, setDeleting] = useState(false);

  // Form state
  const [name, setName] = useState("");
  const [platform, setPlatform] = useState<"slack" | "discord" | "mattermost">("slack");
  const [slackAppToken, setSlackAppToken] = useState("");
  const [slackBotToken, setSlackBotToken] = useState("");
  const [discordToken, setDiscordToken] = useState("");
  const [discordGuildId, setDiscordGuildId] = useState("");
  const [mattermostUrl, setMattermostUrl] = useState("");
  const [mattermostToken, setMattermostToken] = useState("");
  const [mattermostPort, setMattermostPort] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [description, setDescription] = useState("");
  const [personality, setPersonality] = useState("");
  const [repoPath, setRepoPath] = useState("");
  const [githubRepo, setGithubRepo] = useState("");
  const [provider, setProvider] = useState<"claude" | "codex">("claude");
  const [model, setModel] = useState("");
  const [maxTurns, setMaxTurns] = useState(5);
  const [fullAgent, setFullAgent] = useState(false);
  const [contextWindow, setContextWindow] = useState(20);
  const [autoExtract, setAutoExtract] = useState(true);
  const [enabledTools, setEnabledTools] = useState<string[]>([]);
  const [dangerousTools, setDangerousTools] = useState<string[]>([]);
  const [allowedChannels, setAllowedChannels] = useState<string[]>([]);
  const [channelInput, setChannelInput] = useState("");
  const [isActive, setIsActive] = useState(true);

  useEffect(() => {
    fetch(`/api/bots/${id}`)
      .then((r) => (r.ok ? r.json() : Promise.reject()))
      .then((data: BotRaw) => {
        setBot(data);
        setName(data.name);
        setPlatform((data.platform as "slack" | "discord" | "mattermost") ?? "slack");
        setSlackAppToken(data.slackAppToken);
        setSlackBotToken(data.slackBotToken);
        setDiscordToken(data.discord?.token ?? "");
        setDiscordGuildId(data.discord?.guild_id ?? "");
        setMattermostUrl(data.mattermost?.url ?? "");
        setMattermostToken(data.mattermost?.token ?? "");
        setMattermostPort(data.mattermost?.port ? String(data.mattermost.port) : "");
        setDisplayName(data.persona?.display_name ?? "");
        setDescription(data.persona?.description ?? "");
        setPersonality(data.persona?.personality ?? "");
        setRepoPath(data.project?.repo_path ?? "");
        setGithubRepo(data.project?.github_repo ?? "");
        setProvider((data.claude?.provider as "claude" | "codex") ?? "claude");
        setModel(data.claude?.model ?? "claude-opus-4-5");
        setMaxTurns(data.claude?.max_turns ?? 5);
        setFullAgent(data.claude?.full_agent ?? false);
        setContextWindow(data.memory?.context_window ?? 20);
        setAutoExtract(data.memory?.auto_extract ?? true);
        setEnabledTools(data.tools?.enabled ?? []);
        setDangerousTools(data.security?.dangerous_tools ?? []);
        setAllowedChannels(data.security?.allowed_channels ?? []);
        setIsActive(data.isActive);
      })
      .catch(() => setError("봇 정보를 불러오지 못했습니다"))
      .finally(() => setLoading(false));
  }, [id]);

  const toggleTool = (tool: string) =>
    setEnabledTools((prev) =>
      prev.includes(tool) ? prev.filter((t) => t !== tool) : [...prev, tool]
    );

  const toggleDangerousTool = (tool: string) =>
    setDangerousTools((prev) =>
      prev.includes(tool) ? prev.filter((t) => t !== tool) : [...prev, tool]
    );

  const addChannel = () => {
    const ch = channelInput.trim();
    if (ch && !allowedChannels.includes(ch)) {
      setAllowedChannels((prev) => [...prev, ch]);
      setChannelInput("");
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const res = await fetch(`/api/bots/${id}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name,
          platform,
          ...(platform === "discord"
            ? { discord: { token: discordToken, guild_id: discordGuildId }, slackAppToken: "", slackBotToken: "" }
            : platform === "mattermost"
            ? { mattermost: { url: mattermostUrl, token: mattermostToken, port: mattermostPort ? parseInt(mattermostPort) : 8065 }, slackAppToken: "", slackBotToken: "" }
            : { slackAppToken, slackBotToken }),
          channels: allowedChannels,
          isActive,
          persona: { display_name: displayName, description, personality },
          project: { repo_path: repoPath, github_repo: githubRepo },
          claude: { provider, model, max_turns: maxTurns, full_agent: fullAgent },
          memory: { context_window: contextWindow, auto_extract: autoExtract },
          tools: { enabled: enabledTools },
          security: { allowed_channels: allowedChannels, dangerous_tools: dangerousTools },
        }),
      });
      if (!res.ok) {
        const data = await res.json();
        setError(data.error ?? "저장에 실패했습니다");
        return;
      }
      router.push("/bots");
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async () => {
    setDeleting(true);
    try {
      const res = await fetch(`/api/bots/${id}`, { method: "DELETE" });
      if (res.ok) router.push("/bots");
    } finally {
      setDeleting(false);
    }
  };

  if (loading) {
    return (
      <div className="space-y-4 max-w-2xl">
        {[1, 2, 3].map((i) => (
          <div key={i} className="h-32 w-full animate-pulse rounded-xl bg-muted" />
        ))}
      </div>
    );
  }

  if (error && !bot) {
    return (
      <div className="flex flex-col items-center justify-center py-24 text-center">
        <p className="text-sm text-muted-foreground">{error}</p>
        <Link
          href="/bots"
          className={buttonVariants({ variant: "outline", className: "mt-4" })}
        >
          봇 목록으로
        </Link>
      </div>
    );
  }

  return (
    <div className="space-y-6 max-w-2xl">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Link
            href="/bots"
            className={buttonVariants({ variant: "ghost", size: "icon", className: "h-8 w-8" })}
          >
            <ChevronLeft className="h-4 w-4" />
          </Link>
          <div>
            <h1 className="text-2xl font-bold text-foreground">{bot?.name}</h1>
            <p className="text-xs text-muted-foreground font-mono mt-0.5">
              {id}
            </p>
          </div>
        </div>
        <Button
          variant="ghost"
          size="sm"
          className="gap-2 text-destructive hover:text-destructive hover:bg-destructive/10"
          onClick={() => setShowDeleteDialog(true)}
        >
          <Trash2 className="h-4 w-4" />
          삭제
        </Button>
      </div>

      {error && (
        <div className="rounded-lg border border-destructive/50 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          {error}
        </div>
      )}

      <form onSubmit={handleSubmit} className="space-y-4">
        {/* 기본 정보 */}
        <Card className="border-border/50">
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">기본 정보</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label className="text-xs">플랫폼</Label>
              <Select
                value={platform}
                onValueChange={(v) => setPlatform(v as "slack" | "discord" | "mattermost")}
              >
                <SelectTrigger className="h-9 text-sm">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="slack">Slack</SelectItem>
                  <SelectItem value="discord">Discord</SelectItem>
                  <SelectItem value="mattermost">Mattermost</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-2">
                <Label className="text-xs">봇 ID</Label>
                <Input
                  value={id}
                  disabled
                  className="h-9 text-sm font-mono opacity-60"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="edit-name" className="text-xs">
                  봇 이름 <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="edit-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  required
                  className="h-9 text-sm"
                />
              </div>
            </div>
            <label className="flex items-center gap-2 cursor-pointer">
              <input
                type="checkbox"
                checked={isActive}
                onChange={(e) => setIsActive(e.target.checked)}
                className="h-4 w-4 rounded border-border accent-primary"
              />
              <span className="text-sm">활성화</span>
            </label>
          </CardContent>
        </Card>

        {/* 인증 */}
        {platform === "slack" && (
          <Card className="border-border/50">
            <CardHeader className="pb-3">
              <CardTitle className="text-sm">Slack 토큰</CardTitle>
              <CardDescription className="text-xs">
                수정하려면 새 값을 입력하세요
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-2">
                <Label className="text-xs">App Token</Label>
                <div className="relative">
                  <Input
                    type={showAppToken ? "text" : "password"}
                    value={slackAppToken}
                    onChange={(e) => setSlackAppToken(e.target.value)}
                    placeholder={maskToken(bot?.slackAppToken ?? "")}
                    className="h-9 text-sm font-mono pr-10"
                  />
                  <button
                    type="button"
                    className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                    onClick={() => setShowAppToken(!showAppToken)}
                    aria-label={showAppToken ? "토큰 숨기기" : "토큰 보기"}
                  >
                    {showAppToken ? (
                      <EyeOff className="h-3.5 w-3.5" />
                    ) : (
                      <Eye className="h-3.5 w-3.5" />
                    )}
                  </button>
                </div>
              </div>
              <div className="space-y-2">
                <Label className="text-xs">Bot Token</Label>
                <div className="relative">
                  <Input
                    type={showBotToken ? "text" : "password"}
                    value={slackBotToken}
                    onChange={(e) => setSlackBotToken(e.target.value)}
                    placeholder={maskToken(bot?.slackBotToken ?? "")}
                    className="h-9 text-sm font-mono pr-10"
                  />
                  <button
                    type="button"
                    className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                    onClick={() => setShowBotToken(!showBotToken)}
                    aria-label={showBotToken ? "토큰 숨기기" : "토큰 보기"}
                  >
                    {showBotToken ? (
                      <EyeOff className="h-3.5 w-3.5" />
                    ) : (
                      <Eye className="h-3.5 w-3.5" />
                    )}
                  </button>
                </div>
              </div>
            </CardContent>
          </Card>
        )}

        {platform === "discord" && (
          <Card className="border-border/50">
            <CardHeader className="pb-3">
              <CardTitle className="text-sm">Discord 인증</CardTitle>
              <CardDescription className="text-xs">
                수정하려면 새 값을 입력하세요
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="edit-discord-token" className="text-xs">
                  Bot Token <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="edit-discord-token"
                  type="password"
                  value={discordToken}
                  onChange={(e) => setDiscordToken(e.target.value)}
                  placeholder={bot?.discord?.token ? "••••••••" : "MTxxxxxxxxxxxxxxxxxxxxxxxx...."}
                  className="h-9 text-sm font-mono"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="edit-discord-guild-id" className="text-xs">
                  Guild ID
                </Label>
                <Input
                  id="edit-discord-guild-id"
                  value={discordGuildId}
                  onChange={(e) => setDiscordGuildId(e.target.value)}
                  placeholder="123456789012345678"
                  className="h-9 text-sm font-mono"
                />
              </div>
            </CardContent>
          </Card>
        )}

        {platform === "mattermost" && (
          <Card className="border-border/50">
            <CardHeader className="pb-3">
              <CardTitle className="text-sm">Mattermost 인증</CardTitle>
              <CardDescription className="text-xs">
                수정하려면 새 값을 입력하세요
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="edit-mattermost-url" className="text-xs">
                  서버 URL <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="edit-mattermost-url"
                  value={mattermostUrl}
                  onChange={(e) => setMattermostUrl(e.target.value)}
                  placeholder="https://mattermost.example.com"
                  className="h-9 text-sm font-mono"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="edit-mattermost-token" className="text-xs">
                  Bot Token <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="edit-mattermost-token"
                  type="password"
                  value={mattermostToken}
                  onChange={(e) => setMattermostToken(e.target.value)}
                  placeholder={bot?.mattermost?.token ? "••••••••" : "xxxxxxxxxxxxxxxxxxxxxxxxxx"}
                  className="h-9 text-sm font-mono"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="edit-mattermost-port" className="text-xs">
                  포트 (기본값: 8065)
                </Label>
                <Input
                  id="edit-mattermost-port"
                  type="number"
                  value={mattermostPort}
                  onChange={(e) => setMattermostPort(e.target.value)}
                  placeholder="8065"
                  className="h-9 text-sm"
                />
              </div>
            </CardContent>
          </Card>
        )}

        {/* 페르소나 */}
        <Card className="border-border/50">
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">페르소나</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label className="text-xs">표시 이름</Label>
              <Input
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                className="h-9 text-sm"
              />
            </div>
            <div className="space-y-2">
              <Label className="text-xs">설명</Label>
              <Input
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                className="h-9 text-sm"
              />
            </div>
            <div className="space-y-2">
              <Label className="text-xs">성격 / 지시사항</Label>
              <Textarea
                value={personality}
                onChange={(e) => setPersonality(e.target.value)}
                rows={3}
                className="text-sm resize-none"
              />
            </div>
          </CardContent>
        </Card>

        {/* 프로젝트 */}
        <Card className="border-border/50">
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">프로젝트</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label className="text-xs">레포지토리 경로</Label>
              <Input
                value={repoPath}
                onChange={(e) => setRepoPath(e.target.value)}
                className="h-9 text-sm font-mono"
              />
            </div>
            <div className="space-y-2">
              <Label className="text-xs">GitHub 레포지토리</Label>
              <Input
                value={githubRepo}
                onChange={(e) => setGithubRepo(e.target.value)}
                className="h-9 text-sm font-mono"
              />
            </div>
          </CardContent>
        </Card>

        {/* LLM 설정 */}
        <Card className="border-border/50">
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">LLM 설정</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-2">
                <Label className="text-xs">Provider</Label>
                <Select
                  value={provider}
                  onValueChange={(v) => setProvider((v ?? "claude") as "claude" | "codex")}
                >
                  <SelectTrigger className="h-9 text-sm">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="claude">Claude</SelectItem>
                    <SelectItem value="codex">Codex</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-2">
                <Label className="text-xs">모델</Label>
                <Input
                  value={model}
                  onChange={(e) => setModel(e.target.value)}
                  className="h-9 text-sm font-mono"
                />
              </div>
            </div>
            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-2">
                <Label className="text-xs">최대 턴 수</Label>
                <Input
                  type="number"
                  min={1}
                  max={100}
                  value={maxTurns}
                  onChange={(e) => setMaxTurns(parseInt(e.target.value) || 5)}
                  className="h-9 text-sm"
                />
              </div>
              <div className="space-y-2">
                <Label className="text-xs">컨텍스트 윈도우 (최근 메시지 수)</Label>
                <Input
                  type="number"
                  min={1}
                  value={contextWindow}
                  onChange={(e) =>
                    setContextWindow(parseInt(e.target.value) || 20)
                  }
                  className="h-9 text-sm"
                />
              </div>
            </div>
            <div className="flex gap-6">
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={fullAgent}
                  onChange={(e) => setFullAgent(e.target.checked)}
                  className="h-4 w-4 rounded border-border accent-primary"
                />
                <span className="text-sm">Full Agent 모드</span>
                <span title="활성화 시 봇이 도구(tool)를 사용하여 코드 실행, 파일 검색 등 확장된 작업을 수행할 수 있습니다.">
                  <HelpCircle className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                </span>
              </label>
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={autoExtract}
                  onChange={(e) => setAutoExtract(e.target.checked)}
                  className="h-4 w-4 rounded border-border accent-primary"
                />
                <span className="text-sm">메모리 자동 추출</span>
                <span title="대화에서 중요한 정보를 자동으로 추출하여 장기 기억으로 저장합니다.">
                  <HelpCircle className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                </span>
              </label>
            </div>
          </CardContent>
        </Card>

        {/* 도구 */}
        <Card className="border-border/50">
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">활성화할 도구</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="flex flex-wrap gap-2">
              {AVAILABLE_TOOLS.map((tool) => (
                <button
                  key={tool}
                  type="button"
                  onClick={() => toggleTool(tool)}
                  className={`rounded-full px-3 py-1 text-xs font-medium border transition-colors ${
                    enabledTools.includes(tool)
                      ? "bg-primary/15 text-primary border-primary/40"
                      : "bg-transparent text-muted-foreground border-border hover:border-border/80"
                  }`}
                >
                  {tool}
                </button>
              ))}
            </div>
          </CardContent>
        </Card>

        {/* 보안 */}
        <Card className="border-border/50">
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">보안</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label className="text-xs">허용 채널</Label>
              <div className="flex gap-2">
                <Input
                  value={channelInput}
                  onChange={(e) => setChannelInput(e.target.value)}
                  placeholder="C1234567890"
                  className="h-9 text-sm font-mono"
                  onKeyDown={(e) =>
                    e.key === "Enter" && (e.preventDefault(), addChannel())
                  }
                />
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  className="h-9 w-9"
                  onClick={addChannel}
                >
                  <Plus className="h-4 w-4" />
                </Button>
              </div>
              {allowedChannels.length > 0 && (
                <div className="flex flex-wrap gap-1.5 mt-2">
                  {allowedChannels.map((ch) => (
                    <Badge
                      key={ch}
                      variant="secondary"
                      className="gap-1 text-xs"
                    >
                      {ch}
                      <button
                        type="button"
                        onClick={() =>
                          setAllowedChannels((prev) =>
                            prev.filter((c) => c !== ch)
                          )
                        }
                        aria-label={`${ch} 제거`}
                      >
                        <X className="h-2.5 w-2.5" />
                      </button>
                    </Badge>
                  ))}
                </div>
              )}
            </div>

            <Separator className="bg-border/50" />

            <div className="space-y-2">
              <Label className="text-xs">위험 도구 허용</Label>
              <div className="flex flex-wrap gap-2">
                {DANGEROUS_TOOLS.map((tool) => (
                  <button
                    key={tool}
                    type="button"
                    onClick={() => toggleDangerousTool(tool)}
                    className={`rounded-full px-3 py-1 text-xs font-medium border transition-colors ${
                      dangerousTools.includes(tool)
                        ? "bg-destructive/15 text-destructive border-destructive/40"
                        : "bg-transparent text-muted-foreground border-border hover:border-border/80"
                    }`}
                  >
                    {tool}
                  </button>
                ))}
              </div>
            </div>
          </CardContent>
        </Card>

        <div className="flex justify-end gap-3 pb-6">
          <Link
            href="/bots"
            className={buttonVariants({ variant: "outline" })}
          >
            취소
          </Link>
          <Button type="submit" disabled={submitting}>
            {submitting ? "저장 중..." : "변경사항 저장"}
          </Button>
        </div>
      </form>

      {/* Delete Dialog */}
      <Dialog open={showDeleteDialog} onOpenChange={setShowDeleteDialog}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>봇 삭제</DialogTitle>
            <DialogDescription>
              <strong className="text-foreground">{bot?.name}</strong> 봇을
              삭제하시겠습니까? 이 작업은 되돌릴 수 없으며 모든 관련 데이터가
              삭제됩니다.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setShowDeleteDialog(false)}
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
