import * as React from "react";
import { useNavigate } from "react-router-dom";
import { Loader2, Plus } from "lucide-react";
import { toast } from "sonner";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";
import { Button } from "@/components/ui/button";
import {
  ZoneForm,
  emptyZone,
  validateZone,
  toCreateInput,
  type ZoneFormValue,
} from "@/components/zones/zone-form";
import { useCreateZone } from "@/hooks/use-zones";
import { ApiError } from "@/lib/api";

export function AddZoneSheet({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
}) {
  const [form, setForm] = React.useState<ZoneFormValue>(emptyZone);
  const [errors, setErrors] = React.useState<Record<string, string>>({});
  const navigate = useNavigate();
  const create = useCreateZone();

  const close = (o: boolean) => {
    if (!o) {
      setForm(emptyZone);
      setErrors({});
      create.reset();
    }
    onOpenChange(o);
  };

  const submit = () => {
    const errs = validateZone(form, "create");
    setErrors(errs);
    if (Object.keys(errs).length > 0) return;
    create.mutate(toCreateInput(form), {
      onSuccess: (zone) => {
        toast.success(`Zone ${zone.name} created`, {
          description: `CDN hostname: ${zone.cdn_hostname} — copy it on the zone page + CNAME your domain to it.`,
        });
        close(false);
        navigate(`/zones/${zone.id}`);
      },
      onError: (e) => {
        if (e instanceof ApiError && e.status === 409) {
          setErrors((prev) => ({ ...prev, cdn_hostname: "This CDN hostname is already taken." }));
        } else {
          toast.error("Couldn't create zone", { description: (e as Error).message });
        }
      },
    });
  };

  return (
    <Sheet open={open} onOpenChange={close}>
      <SheetContent side="right" className="w-full overflow-y-auto sm:w-[460px]">
        <SheetTitle>Add zone</SheetTitle>
        <form
          className="space-y-5"
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <p className="text-sm text-muted-foreground">
            A pull zone Brisk accelerates: clients hit the CDN hostname, the edge pulls from your origin.
          </p>
          <ZoneForm value={form} onChange={setForm} errors={errors} mode="create" />
          <Button type="submit" className="w-full" disabled={create.isPending}>
            {create.isPending ? <Loader2 className="animate-spin" /> : <Plus />}
            Create zone
          </Button>
        </form>
      </SheetContent>
    </Sheet>
  );
}
