"use client";

import { useEffect, useState, useCallback } from "react";
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Badge } from "@/components/ui/badge";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Button } from "@/components/ui/button";
import { MessageSquare, DollarSign, Layers, RefreshCw } from "lucide-react";

interface Message {
  id: string;
  botId: string;
  userId: string;
  channelId: string;
  role: string;
  content: string;
  timestamp: string;
  bot: { id: string; name: string };
}

interface UsageStat {
  botId: string;
  botName: string;
  model: string;
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
  costUsd: number;
  calls: number;
}

interface Bot {
  id: string;
  name: string;
}

export default function MonitoringPage() {
  const [bots, setBots] = useState<Bot[]>([]);
  const [selectedBotId, setSelectedBotId] = useState<string>("all");
  const [messages, setMessages] = useState<Message[]>([]);
  const [usageStats, setUsageStats] = useState<UsageStat[]>([]);
  const [totalMessages, setTotalMessages] = useState(0);
  const [totalCostUsd, setTotalCostUsd] = useState(0);
  const [totalTokens, setTotalTokens] = useState(0);
  const [days, setDays] = useState("7");
  const [loadingMessages, setLoadingMessages] = useState(true);
  const [loadingUsage, setLoadingUsage] = useState(true);

  useEffect(() => {
    fetch("/api/bots")
      .then((r) => r.json())
      .then(setBots)
      .catch(() => setBots([]));
  }, []);

  const fetchMessages = useCallback(() => {
    setLoadingMessages(true);
    const botParam =
      selectedBotId !== "all" ? `&botId=${selectedBotId}` : "";
    fetch(`/api/messages?limit=100${botParam}`)
      .then((r) => r.json())
      .then(setMessages)
      .catch(() => setMessages([]))
      .finally(() => setLoadingMessages(false));
  }, [selectedBotId]);

  const fetchUsage = useCallback(() => {
    setLoadingUsage(true);
    const botParam =
      selectedBotId !== "all" ? `&botId=${selectedBotId}` : "";
    fetch(`/api/usage?days=${days}${botParam}`)
      .then((r) => r.json())
      .then((data) => {
        setUsageStats(data.stats ?? []);
        setTotalMessages(data.totalMessages ?? 0);
        setTotalCostUsd(data.totalCostUsd ?? 0);
        setTotalTokens(data.totalTokens ?? 0);
      })
      .catch(() => {
        setUsageStats([]);
        setTotalMessages(0);
        setTotalCostUsd(0);
        setTotalTokens(0);
      })
      .finally(() => setLoadingUsage(false));
  }, [selectedBotId, days]);

  useEffect(() => {
    fetchMessages();
    fetchUsage();
  }, [fetchMessages, fetchUsage]);

  const roleBadgeClass = (role: string) => {
    if (role === "assistant")
      return "bg-primary/15 text-primary border-0 hover:bg-primary/20";
    if (role === "user")
      return "bg-muted text-muted-foreground";
    return "";
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">모니터링</h1>
          <p className="text-sm text-muted-foreground mt-1">
            대화 로그 및 API 사용량
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            fetchMessages();
            fetchUsage();
          }}
          className="gap-2"
        >
          <RefreshCw className="h-4 w-4" />
          새로고침
        </Button>
      </div>

      {/* Filters */}
      <div className="flex flex-wrap gap-3">
        <Select
          value={selectedBotId}
          onValueChange={(v) => setSelectedBotId(v ?? "all")}
        >
          <SelectTrigger className="h-9 w-48 text-sm">
            <SelectValue placeholder="봇 선택" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">모든 봇</SelectItem>
            {bots.map((b) => (
              <SelectItem key={b.id} value={b.id}>
                {b.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <Select value={days} onValueChange={(v) => setDays(v ?? "7")}>
          <SelectTrigger className="h-9 w-36 text-sm">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="1">최근 1일</SelectItem>
            <SelectItem value="7">최근 7일</SelectItem>
            <SelectItem value="30">최근 30일</SelectItem>
            <SelectItem value="90">최근 90일</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <Tabs defaultValue="messages">
        <TabsList className="bg-muted/50">
          <TabsTrigger value="messages" className="text-sm">
            대화 로그
          </TabsTrigger>
          <TabsTrigger value="usage" className="text-sm">
            API 사용량
          </TabsTrigger>
        </TabsList>

        {/* 대화 로그 탭 */}
        <TabsContent value="messages" className="mt-4">
          <Card className="border-border/50">
            <CardHeader className="pb-3">
              <CardTitle className="text-base">대화 로그</CardTitle>
              <CardDescription className="text-xs">
                최근 100개 메시지
              </CardDescription>
            </CardHeader>
            <CardContent className="px-0">
              {loadingMessages ? (
                <div className="space-y-2 px-6 pb-4">
                  {[1, 2, 3, 4, 5].map((i) => (
                    <div
                      key={i}
                      className="h-10 w-full animate-pulse rounded bg-muted"
                    />
                  ))}
                </div>
              ) : messages.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-16 text-center">
                  <MessageSquare className="h-12 w-12 text-muted-foreground/30 mb-4" />
                  <p className="text-sm text-muted-foreground">
                    대화 기록이 없습니다
                  </p>
                </div>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow className="border-border/50 hover:bg-transparent">
                      <TableHead className="text-xs w-36">시각</TableHead>
                      <TableHead className="text-xs w-24">봇</TableHead>
                      <TableHead className="text-xs w-28 hidden sm:table-cell">
                        사용자
                      </TableHead>
                      <TableHead className="text-xs w-20">역할</TableHead>
                      <TableHead className="text-xs">내용</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {messages.map((msg) => (
                      <TableRow
                        key={msg.id}
                        className="border-border/30 hover:bg-muted/20"
                      >
                        <TableCell className="text-xs text-muted-foreground whitespace-nowrap">
                          {new Date(msg.timestamp).toLocaleString("ko-KR", {
                            month: "2-digit",
                            day: "2-digit",
                            hour: "2-digit",
                            minute: "2-digit",
                          })}
                        </TableCell>
                        <TableCell>
                          <span className="text-xs font-medium">
                            {msg.bot?.name ?? msg.botId}
                          </span>
                        </TableCell>
                        <TableCell className="hidden sm:table-cell">
                          <span className="text-xs text-muted-foreground font-mono">
                            {msg.userId}
                          </span>
                        </TableCell>
                        <TableCell>
                          <Badge
                            variant="outline"
                            className={`text-xs ${roleBadgeClass(msg.role)}`}
                          >
                            {msg.role}
                          </Badge>
                        </TableCell>
                        <TableCell>
                          <p className="text-xs text-muted-foreground line-clamp-2 max-w-xs">
                            {msg.content}
                          </p>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>
        </TabsContent>

        {/* API 사용량 탭 */}
        <TabsContent value="usage" className="mt-4 space-y-4">
          {/* Summary Cards */}
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
            <Card className="border-border/50">
              <CardContent className="flex items-center gap-3 p-4">
                <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/15">
                  <MessageSquare className="h-4 w-4 text-primary" />
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">총 메시지</p>
                  {loadingUsage ? (
                    <div className="mt-1 h-5 w-16 animate-pulse rounded bg-muted" />
                  ) : (
                    <p className="text-lg font-bold">
                      {totalMessages.toLocaleString()}
                    </p>
                  )}
                </div>
              </CardContent>
            </Card>

            <Card className="border-border/50">
              <CardContent className="flex items-center gap-3 p-4">
                <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/15">
                  <Layers className="h-4 w-4 text-primary" />
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">총 토큰</p>
                  {loadingUsage ? (
                    <div className="mt-1 h-5 w-16 animate-pulse rounded bg-muted" />
                  ) : (
                    <p className="text-lg font-bold">
                      {totalTokens >= 1000000
                        ? `${(totalTokens / 1000000).toFixed(1)}M`
                        : totalTokens >= 1000
                          ? `${(totalTokens / 1000).toFixed(1)}K`
                          : totalTokens.toLocaleString()}
                    </p>
                  )}
                </div>
              </CardContent>
            </Card>

            <Card className="border-border/50 col-span-2 sm:col-span-1">
              <CardContent className="flex items-center gap-3 p-4">
                <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/15">
                  <DollarSign className="h-4 w-4 text-primary" />
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">총 비용 (USD)</p>
                  {loadingUsage ? (
                    <div className="mt-1 h-5 w-16 animate-pulse rounded bg-muted" />
                  ) : (
                    <p className="text-lg font-bold">
                      ${totalCostUsd.toFixed(4)}
                    </p>
                  )}
                </div>
              </CardContent>
            </Card>
          </div>

          {/* Usage Table */}
          <Card className="border-border/50">
            <CardHeader className="pb-3">
              <CardTitle className="text-base">모델별 사용량</CardTitle>
              <CardDescription className="text-xs">
                최근 {days}일 기준
              </CardDescription>
            </CardHeader>
            <CardContent className="px-0">
              {loadingUsage ? (
                <div className="space-y-2 px-6 pb-4">
                  {[1, 2, 3].map((i) => (
                    <div
                      key={i}
                      className="h-10 w-full animate-pulse rounded bg-muted"
                    />
                  ))}
                </div>
              ) : usageStats.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-16 text-center">
                  <DollarSign className="h-12 w-12 text-muted-foreground/30 mb-4" />
                  <p className="text-sm text-muted-foreground">
                    사용 기록이 없습니다
                  </p>
                </div>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow className="border-border/50 hover:bg-transparent">
                      <TableHead className="text-xs">봇</TableHead>
                      <TableHead className="text-xs">모델</TableHead>
                      <TableHead className="text-xs text-right">
                        호출 수
                      </TableHead>
                      <TableHead className="text-xs text-right hidden sm:table-cell">
                        입력 토큰
                      </TableHead>
                      <TableHead className="text-xs text-right hidden sm:table-cell">
                        출력 토큰
                      </TableHead>
                      <TableHead className="text-xs text-right">
                        비용 (USD)
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {usageStats.map((stat, i) => (
                      <TableRow
                        key={`${stat.botId}-${stat.model}-${i}`}
                        className="border-border/30 hover:bg-muted/20"
                      >
                        <TableCell>
                          <span className="text-sm font-medium">
                            {stat.botName}
                          </span>
                        </TableCell>
                        <TableCell>
                          <span className="text-xs font-mono text-muted-foreground">
                            {stat.model}
                          </span>
                        </TableCell>
                        <TableCell className="text-right">
                          <span className="text-sm">
                            {stat.calls.toLocaleString()}
                          </span>
                        </TableCell>
                        <TableCell className="text-right hidden sm:table-cell">
                          <span className="text-sm text-muted-foreground">
                            {stat.inputTokens.toLocaleString()}
                          </span>
                        </TableCell>
                        <TableCell className="text-right hidden sm:table-cell">
                          <span className="text-sm text-muted-foreground">
                            {stat.outputTokens.toLocaleString()}
                          </span>
                        </TableCell>
                        <TableCell className="text-right">
                          <span className="text-sm font-mono">
                            ${stat.costUsd.toFixed(4)}
                          </span>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  );
}
