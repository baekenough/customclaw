"use client";

import { signIn } from "next-auth/react";
import { Fingerprint, Github } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

export default function LoginPage() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4">
      <div className="w-full max-w-sm space-y-8">
        {/* Branding */}
        <div className="flex flex-col items-center gap-3">
          <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-primary/15 ring-1 ring-primary/30">
            <Fingerprint className="h-7 w-7 text-primary" />
          </div>
          <div className="text-center">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">
              CustomClaw
            </h1>
            <p className="mt-1 text-sm text-muted-foreground">
              Slack Bot Management Platform
            </p>
          </div>
        </div>

        {/* Login Card */}
        <Card className="border-border/50 bg-card/80 backdrop-blur-sm">
          <CardHeader className="text-center pb-4">
            <CardTitle className="text-base font-semibold">로그인</CardTitle>
            <CardDescription className="text-xs">
              GitHub 계정으로 계속하세요
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button
              className="w-full gap-2"
              onClick={() => signIn("github", { callbackUrl: "/" })}
            >
              <Github className="h-4 w-4" />
              GitHub으로 로그인
            </Button>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
