import * as React from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { KeyRound, LogOut, ShieldCheck, Copy, Trash2, Plus } from "lucide-react";
import { toast } from "sonner";
import { PageHeader } from "@/components/layout/page-header";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAuth } from "@/app/auth";
import { api, ApiError } from "@/lib/api";

interface AdminToken {
  id: number;
  name: string;
  prefix: string;
  created_at: string;
  last_used_at?: string | null;
  revoked_at?: string | null;
}

export default function SettingsPage() {
  const { user, logout } = useAuth();
  const qc = useQueryClient();

  // --- change password ---
  const [curPw, setCurPw] = React.useState("");
  const [newPw, setNewPw] = React.useState("");
  const changePw = useMutation({
    mutationFn: () => api.post("/admin/password", { current_password: curPw, new_password: newPw }),
    onSuccess: () => {
      toast.success("Password changed");
      setCurPw("");
      setNewPw("");
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "Could not change password"),
  });

  // --- admin API tokens ---
  const tokensQ = useQuery({
    queryKey: ["admin-tokens"],
    queryFn: () => api.get<AdminToken[]>("/admin/tokens"),
  });
  const [tokenName, setTokenName] = React.useState("");
  const [newToken, setNewToken] = React.useState<string | null>(null);
  const createToken = useMutation({
    mutationFn: () => api.post<{ token: string }>("/admin/tokens", { name: tokenName }),
    onSuccess: (res) => {
      setNewToken(res.token); // shown ONCE
      setTokenName("");
      qc.invalidateQueries({ queryKey: ["admin-tokens"] });
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "Could not create token"),
  });
  const revokeToken = useMutation({
    mutationFn: (id: number) => api.del(`/admin/tokens/${id}`),
    onSuccess: () => {
      toast.success("Token revoked");
      qc.invalidateQueries({ queryKey: ["admin-tokens"] });
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "Could not revoke token"),
  });
  // Permanently remove an already-revoked token from the list (tidy-up).
  const deleteToken = useMutation({
    mutationFn: (id: number) => api.del(`/admin/tokens/${id}/purge`),
    onSuccess: () => {
      toast.success("Token deleted");
      qc.invalidateQueries({ queryKey: ["admin-tokens"] });
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "Could not delete token"),
  });

  return (
    <div className="space-y-5">
      <PageHeader title="Settings" description="Account, security, and API access." />

      {/* Account */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <ShieldCheck className="size-4 text-muted-foreground" /> Account
            {user && (
              <Badge variant="muted" className="ml-1 uppercase">
                {user.role}
              </Badge>
            )}
          </CardTitle>
          <p className="text-xs text-muted-foreground">{user?.email || user?.name}</p>
        </CardHeader>
        <CardContent>
          <Button variant="outline" size="sm" onClick={() => logout()}>
            <LogOut className="size-4" /> Sign out
          </Button>
        </CardContent>
      </Card>

      {/* Change password */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <KeyRound className="size-4 text-muted-foreground" /> Change password
          </CardTitle>
          <p className="text-xs text-muted-foreground">
            Passwords are stored with argon2id. Changing it signs out other sessions.
          </p>
        </CardHeader>
        <CardContent>
          <form
            className="grid max-w-sm gap-3"
            onSubmit={(e) => {
              e.preventDefault();
              changePw.mutate();
            }}
          >
            <div className="space-y-1.5">
              <Label htmlFor="cur">Current password</Label>
              <Input id="cur" type="password" value={curPw} onChange={(e) => setCurPw(e.target.value)} required />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="new">New password (min 10 chars)</Label>
              <Input id="new" type="password" value={newPw} onChange={(e) => setNewPw(e.target.value)} required minLength={10} />
            </div>
            <Button type="submit" size="sm" disabled={changePw.isPending} className="justify-self-start">
              {changePw.isPending ? "Saving…" : "Update password"}
            </Button>
          </form>
        </CardContent>
      </Card>

      {/* Admin API tokens */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <KeyRound className="size-4 text-muted-foreground" /> Admin API tokens
          </CardTitle>
          <p className="text-xs text-muted-foreground">
            Bearer tokens for scripts/automation. Shown once at creation, hashed at rest, revocable.
            Separate from agent tokens.
          </p>
        </CardHeader>
        <CardContent className="space-y-4">
          {newToken && (
            <div className="rounded-lg border border-primary/40 bg-primary/5 p-3 text-sm">
              <p className="mb-1 font-medium">Copy this token now — it won’t be shown again.</p>
              <div className="flex items-center gap-2">
                <code className="flex-1 truncate rounded bg-background px-2 py-1 text-xs">{newToken}</code>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    navigator.clipboard?.writeText(newToken);
                    toast.success("Copied");
                  }}
                >
                  <Copy className="size-4" />
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setNewToken(null)}>
                  Done
                </Button>
              </div>
            </div>
          )}

          <form
            className="flex items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              createToken.mutate();
            }}
          >
            <div className="space-y-1.5">
              <Label htmlFor="tname">New token name</Label>
              <Input
                id="tname"
                placeholder="e.g. ci-deploy"
                value={tokenName}
                onChange={(e) => setTokenName(e.target.value)}
                className="w-56"
              />
            </div>
            <Button type="submit" size="sm" disabled={createToken.isPending}>
              <Plus className="size-4" /> Create
            </Button>
          </form>

          <div className="divide-y divide-border rounded-lg border border-border">
            {tokensQ.data && tokensQ.data.length > 0 ? (
              tokensQ.data.map((t) => (
                <div key={t.id} className="flex items-center justify-between gap-3 px-3 py-2 text-sm">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="font-medium">{t.name || "(unnamed)"}</span>
                      <code className="text-xs text-muted-foreground">{t.prefix}…</code>
                      {t.revoked_at && (
                        <Badge variant="muted" className="text-[10px]">
                          revoked
                        </Badge>
                      )}
                    </div>
                    <div className="text-xs text-muted-foreground">
                      created {new Date(t.created_at).toLocaleString()}
                      {t.last_used_at ? ` · last used ${new Date(t.last_used_at).toLocaleString()}` : " · never used"}
                    </div>
                  </div>
                  {!t.revoked_at ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-destructive hover:text-destructive"
                      onClick={() => revokeToken.mutate(t.id)}
                      disabled={revokeToken.isPending}
                    >
                      <Trash2 className="size-4" /> Revoke
                    </Button>
                  ) : (
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-muted-foreground hover:text-destructive"
                      title="Permanently remove this revoked token from the list"
                      onClick={() => deleteToken.mutate(t.id)}
                      disabled={deleteToken.isPending}
                    >
                      <Trash2 className="size-4" /> Delete
                    </Button>
                  )}
                </div>
              ))
            ) : (
              <div className="px-3 py-6 text-center text-sm text-muted-foreground">No API tokens yet.</div>
            )}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
