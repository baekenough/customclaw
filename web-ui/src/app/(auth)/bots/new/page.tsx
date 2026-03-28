"use client";

import { useState } from "react";
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
import { ChevronLeft, X, Plus, HelpCircle } from "lucide-react";
import Link from "next/link";
import { buttonVariants } from "@/components/ui/button";
import { AVAILABLE_TOOLS, DANGEROUS_TOOLS } from "@/lib/bot-tools";

export default function NewBotPage() {
  const router = useRouter();
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Form state
  const [id, setId] = useState("");
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
  const [provider, setProvider] = useState<"claude" | "claude-cli" | "codex">("claude");
  const [model, setModel] = useState("claude-opus-4-5");
  const [maxTurns, setMaxTurns] = useState(5);
  const [fullAgent, setFullAgent] = useState(false);
  const [contextWindow, setContextWindow] = useState(20);
  const [autoExtract, setAutoExtract] = useState(true);
  const [enabledTools, setEnabledTools] = useState<string[]>([]);
  const [selectedDangerousTools, setSelectedDangerousTools] = useState<
    string[]
  >([]);
  const [allowedChannels, setAllowedChannels] = useState<string[]>([]);
  const [channelInput, setChannelInput] = useState("");

  const toggleTool = (tool: string) => {
    setEnabledTools((prev) =>
      prev.includes(tool) ? prev.filter((t) => t !== tool) : [...prev, tool]
    );
  };

  const toggleDangerousTool = (tool: string) => {
    setSelectedDangerousTools((prev) =>
      prev.includes(tool) ? prev.filter((t) => t !== tool) : [...prev, tool]
    );
  };

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
      const platformPayload =
        platform === "discord"
          ? { discord: { token: discordToken, guild_id: discordGuildId }, slackAppToken: "", slackBotToken: "" }
          : platform === "mattermost"
          ? { mattermost: { url: mattermostUrl, token: mattermostToken, port: mattermostPort ? parseInt(mattermostPort) : 8065 }, slackAppToken: "", slackBotToken: "" }
          : { slackAppToken, slackBotToken };

      const res = await fetch("/api/bots", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          id,
          name,
          platform,
          ...platformPayload,
          channels: allowedChannels,
          persona: { display_name: displayName, description, personality },
          project: { repo_path: repoPath, github_repo: githubRepo },
          claude: { provider, model, max_turns: maxTurns, full_agent: fullAgent },
          memory: { context_window: contextWindow, auto_extract: autoExtract },
          tools: { enabled: enabledTools },
          security: {
            allowed_channels: allowedChannels,
            dangerous_tools: selectedDangerousTools,
          },
        }),
      });

      if (!res.ok) {
        const data = await res.json();
        setError(data.error ?? "봇 생성에 실패했습니다");
        return;
      }

      router.push("/bots");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="space-y-6 max-w-2xl">
      <div className="flex items-center gap-3">
        <Link
          href="/bots"
          className={buttonVariants({ variant: "ghost", size: "icon", className: "h-8 w-8" })}
        >
          <ChevronLeft className="h-4 w-4" />
        </Link>
        <div>
          <h1 className="text-2xl font-bold text-foreground">새 봇 만들기</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            새로운 봇을 등록합니다
          </p>
        </div>
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
              <Label htmlFor="platform" className="text-xs">
                플랫폼 <span className="text-destructive">*</span>
              </Label>
              <Select
                value={platform}
                onValueChange={(v) => setPlatform(v as "slack" | "discord" | "mattermost")}
              >
                <SelectTrigger id="platform" className="h-9 text-sm">
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
                <Label htmlFor="bot-id" className="text-xs">
                  봇 ID <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="bot-id"
                  value={id}
                  onChange={(e) => setId(e.target.value)}
                  placeholder="my-bot"
                  required
                  className="h-9 text-sm"
                />
                <p className="text-xs text-muted-foreground">
                  소문자, 숫자, 하이픈만 사용
                </p>
              </div>
              <div className="space-y-2">
                <Label htmlFor="bot-name" className="text-xs">
                  봇 이름 <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="bot-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="My Bot"
                  required
                  className="h-9 text-sm"
                />
              </div>
            </div>
          </CardContent>
        </Card>

        {/* 인증 */}
        {platform === "slack" && (
          <Card className="border-border/50">
            <CardHeader className="pb-3">
              <CardTitle className="text-sm">Slack 토큰</CardTitle>
              <CardDescription className="text-xs">
                Slack App 설정에서 발급받은 토큰
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="app-token" className="text-xs">
                  App Token <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="app-token"
                  type="password"
                  value={slackAppToken}
                  onChange={(e) => setSlackAppToken(e.target.value)}
                  placeholder="xapp-..."
                  required
                  className="h-9 text-sm font-mono"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="bot-token" className="text-xs">
                  Bot Token <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="bot-token"
                  type="password"
                  value={slackBotToken}
                  onChange={(e) => setSlackBotToken(e.target.value)}
                  placeholder="xoxb-..."
                  required
                  className="h-9 text-sm font-mono"
                />
              </div>
            </CardContent>
          </Card>
        )}

        {platform === "discord" && (
          <Card className="border-border/50">
            <CardHeader className="pb-3">
              <CardTitle className="text-sm">Discord 인증</CardTitle>
              <CardDescription className="text-xs">
                Discord Developer Portal에서 발급받은 봇 토큰
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="discord-token" className="text-xs">
                  Bot Token <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="discord-token"
                  type="password"
                  value={discordToken}
                  onChange={(e) => setDiscordToken(e.target.value)}
                  placeholder="MTxxxxxxxxxxxxxxxxxxxxxxxx...."
                  required
                  className="h-9 text-sm font-mono"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="discord-guild-id" className="text-xs">
                  Guild ID
                </Label>
                <Input
                  id="discord-guild-id"
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
                Mattermost 서버 연결 정보
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="mattermost-url" className="text-xs">
                  서버 URL <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="mattermost-url"
                  value={mattermostUrl}
                  onChange={(e) => setMattermostUrl(e.target.value)}
                  placeholder="https://mattermost.example.com"
                  required
                  className="h-9 text-sm font-mono"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="mattermost-token" className="text-xs">
                  Bot Token <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="mattermost-token"
                  type="password"
                  value={mattermostToken}
                  onChange={(e) => setMattermostToken(e.target.value)}
                  placeholder="xxxxxxxxxxxxxxxxxxxxxxxxxx"
                  required
                  className="h-9 text-sm font-mono"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="mattermost-port" className="text-xs">
                  포트 (기본값: 8065)
                </Label>
                <Input
                  id="mattermost-port"
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
              <Label htmlFor="display-name" className="text-xs">
                표시 이름
              </Label>
              <Input
                id="display-name"
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                placeholder="CustomClaw Bot"
                className="h-9 text-sm"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="desc" className="text-xs">
                설명
              </Label>
              <Input
                id="desc"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="개발 보조 봇"
                className="h-9 text-sm"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="personality" className="text-xs">
                성격 / 지시사항
              </Label>
              <Textarea
                id="personality"
                value={personality}
                onChange={(e) => setPersonality(e.target.value)}
                placeholder="당신은 친절하고 유능한 개발 도우미입니다..."
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
              <Label htmlFor="repo-path" className="text-xs">
                레포지토리 경로
              </Label>
              <Input
                id="repo-path"
                value={repoPath}
                onChange={(e) => setRepoPath(e.target.value)}
                placeholder="/workspace/my-project"
                className="h-9 text-sm font-mono"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="github-repo" className="text-xs">
                GitHub 레포지토리
              </Label>
              <Input
                id="github-repo"
                value={githubRepo}
                onChange={(e) => setGithubRepo(e.target.value)}
                placeholder="org/repo"
                className="h-9 text-sm font-mono"
              />
            </div>
          </CardContent>
        </Card>

        {/* Claude 설정 */}
        <Card className="border-border/50">
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">LLM 설정</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-2">
                <Label className="text-xs">Provider</Label>
                <p className="text-[10px] text-muted-foreground -mt-1">
                  claude = 자동감지, claude-cli = CLI 강제
                </p>
                <Select
                  value={provider}
                  onValueChange={(v) =>
                    setProvider((v ?? "claude") as "claude" | "claude-cli" | "codex")
                  }
                >
                  <SelectTrigger className="h-9 text-sm">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="claude">Claude (Auto-detect)</SelectItem>
                    <SelectItem value="claude-cli">Claude CLI</SelectItem>
                    <SelectItem value="codex">Codex</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-2">
                <Label htmlFor="model" className="text-xs">
                  모델
                </Label>
                <Input
                  id="model"
                  value={model}
                  onChange={(e) => setModel(e.target.value)}
                  placeholder="claude-opus-4-5"
                  className="h-9 text-sm font-mono"
                />
              </div>
            </div>
            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-2">
                <Label htmlFor="max-turns" className="text-xs">
                  최대 턴 수
                </Label>
                <Input
                  id="max-turns"
                  type="number"
                  min={1}
                  max={100}
                  value={maxTurns}
                  onChange={(e) => setMaxTurns(parseInt(e.target.value) || 5)}
                  className="h-9 text-sm"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="context-window" className="text-xs">
                  컨텍스트 윈도우 (최근 메시지 수)
                </Label>
                <Input
                  id="context-window"
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
            {/* Allowed Channels */}
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

            {/* Dangerous Tools */}
            <div className="space-y-2">
              <Label className="text-xs">위험 도구 허용</Label>
              <p className="text-xs text-muted-foreground">
                선택한 도구는 사용자 승인 없이 실행될 수 있습니다
              </p>
              <div className="flex flex-wrap gap-2">
                {DANGEROUS_TOOLS.map((tool) => (
                  <button
                    key={tool}
                    type="button"
                    onClick={() => toggleDangerousTool(tool)}
                    className={`rounded-full px-3 py-1 text-xs font-medium border transition-colors ${
                      selectedDangerousTools.includes(tool)
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

        {/* Submit */}
        <div className="flex justify-end gap-3 pb-6">
          <Link
            href="/bots"
            className={buttonVariants({ variant: "outline" })}
          >
            취소
          </Link>
          <Button type="submit" disabled={submitting}>
            {submitting ? "생성 중..." : "봇 만들기"}
          </Button>
        </div>
      </form>
    </div>
  );
}
