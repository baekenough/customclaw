"use client";

import { signOut } from "next-auth/react";
import { useState } from "react";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import {
  CircleHelp,
  ExternalLink,
  LogOut,
  MessageSquarePlus,
  User,
} from "lucide-react";

interface TopbarProps {
  user: {
    name?: string | null;
    email?: string | null;
    image?: string | null;
  };
}

function FeedbackButton() {
  const [open, setOpen] = useState(false);
  const [content, setContent] = useState("");
  const [type, setType] = useState<string>("improvement");
  const [submitting, setSubmitting] = useState(false);
  const [submitted, setSubmitted] = useState(false);

  const handleSubmit = async () => {
    if (!content.trim()) return;
    setSubmitting(true);
    try {
      await fetch("/api/feedback", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ type, content }),
      });
      setSubmitted(true);
      setTimeout(() => {
        setOpen(false);
        setSubmitted(false);
        setContent("");
      }, 1500);
    } catch {
      // silently fail
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger
        className="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
        aria-label="피드백 보내기"
      >
        <MessageSquarePlus className="h-4 w-4" />
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>피드백 보내기</DialogTitle>
          <DialogDescription>
            버그 제보, 기능 요청, 개선 아이디어 등을 자유롭게 남겨주세요.
          </DialogDescription>
        </DialogHeader>
        {submitted ? (
          <div className="py-8 text-center text-sm text-muted-foreground">
            피드백이 전송되었습니다. 감사합니다!
          </div>
        ) : (
          <>
            <div className="flex gap-2 mb-3">
              {[
                { label: "버그", value: "bug" },
                { label: "기능 요청", value: "feature" },
                { label: "개선", value: "improvement" },
              ].map((t) => (
                <button
                  key={t.value}
                  type="button"
                  onClick={() => setType(t.value)}
                  className={`px-3 py-1 text-xs font-medium rounded-md transition-all ${
                    type === t.value
                      ? "bg-primary text-primary-foreground"
                      : "bg-muted text-muted-foreground hover:text-foreground"
                  }`}
                >
                  {t.label}
                </button>
              ))}
            </div>
            <Textarea
              placeholder="피드백 내용을 입력하세요..."
              value={content}
              onChange={(e) => setContent(e.target.value)}
              rows={4}
            />
          </>
        )}
        <DialogFooter>
          {!submitted && (
            <Button
              onClick={handleSubmit}
              disabled={!content.trim() || submitting}
              size="sm"
            >
              {submitting ? "전송 중..." : "전송"}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function Topbar({ user }: TopbarProps) {
  return (
    <header className="flex h-16 items-center justify-end gap-1 px-6 border-b border-border bg-background">
      {/* Help dropdown */}
      <DropdownMenu>
        <DropdownMenuTrigger
          className="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          aria-label="도움말"
        >
          <CircleHelp className="h-4 w-4" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-56">
          <DropdownMenuLabel>도움말</DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            className="cursor-pointer"
            onClick={() => window.open("https://github.com/baekenough/customclaw#readme", "_blank")}
          >
            <ExternalLink className="mr-2 h-4 w-4" />
            사용 가이드 (README)
          </DropdownMenuItem>
          <DropdownMenuItem
            className="cursor-pointer"
            onClick={() => window.open("https://github.com/baekenough/customclaw/blob/develop/FOR-AGENTS.md", "_blank")}
          >
            <ExternalLink className="mr-2 h-4 w-4" />
            AI Agent 설정 가이드
          </DropdownMenuItem>
          <DropdownMenuItem
            className="cursor-pointer"
            onClick={() => window.open("https://github.com/baekenough/customclaw/issues", "_blank")}
          >
            <ExternalLink className="mr-2 h-4 w-4" />
            이슈 트래커
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      {/* Feedback dialog */}
      <FeedbackButton />

      {/* User menu */}
      <DropdownMenu>
        <DropdownMenuTrigger
          className="flex items-center gap-2 rounded-full focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          aria-label="사용자 메뉴"
        >
          <Avatar className="h-8 w-8">
            <AvatarImage
              src={user.image ?? undefined}
              alt={user.name ?? "사용자"}
            />
            <AvatarFallback>
              <User className="h-4 w-4" />
            </AvatarFallback>
          </Avatar>
          <span className="hidden sm:block text-sm font-medium text-foreground">
            {user.name}
          </span>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-48">
          <DropdownMenuLabel className="font-normal">
            <div className="flex flex-col space-y-1">
              <p className="text-sm font-medium">{user.name}</p>
              <p className="text-xs text-muted-foreground">{user.email}</p>
            </div>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            className="cursor-pointer text-destructive focus:text-destructive"
            onClick={() => signOut({ callbackUrl: "/login" })}
          >
            <LogOut className="mr-2 h-4 w-4" />
            로그아웃
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </header>
  );
}
