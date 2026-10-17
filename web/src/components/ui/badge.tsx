import type { HTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const badge = cva("inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 text-[11px] font-medium leading-none", {
  variants: {
    variant: {
      default: "border-transparent bg-accent text-accent-foreground",
      outline: "text-muted-foreground",
      ok: "border-transparent bg-ok/15 text-ok",
      warn: "border-transparent bg-warn/15 text-warn",
      bad: "border-transparent bg-bad/15 text-bad",
      shape: "border-transparent bg-shape/15 text-shape",
    },
  },
  defaultVariants: { variant: "default" },
});

export function Badge({ className, variant, ...p }: HTMLAttributes<HTMLSpanElement> & VariantProps<typeof badge>) {
  return <span className={cn(badge({ variant }), className)} {...p} />;
}

// A confidence in [0,1] as a colored badge.
export function Confidence({ value }: { value?: number }) {
  if (value == null) return null;
  const v = value >= 0.8 ? "ok" : value >= 0.5 ? "warn" : "bad";
  return <Badge variant={v}>{Math.round(value * 100)}%</Badge>;
}
