import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

/**
 * Button — shadcn-shaped, bound to this app's tokens.
 *
 * `primary` is the one scan/commit action per screen. Resist adding a second
 * on the same surface; the pre-overhaul UI had several competing CTAs per tab
 * and it was never obvious which one pulled data.
 */
const buttonVariants = cva(
  cn(
    "inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-sm",
    "font-ui font-medium transition-colors",
    "focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-eve-accent",
    "disabled:pointer-events-none disabled:opacity-50",
  ),
  {
    variants: {
      variant: {
        /* accent-dim rather than accent: at full strength the accent is a
           very bright fill for an element this size -- amarr dark measures
           70% luminance -- and it was reported as glaring twice. Hover
           brightens by 5%, the most that keeps every palette above the AA
           floor against its own foreground. */
        primary: "bg-eve-accent-dim text-eve-on-accent-dim hover:brightness-105",
        secondary: "bg-surface-3 text-fg hover:bg-surface-2 border border-eve-border",
        ghost: "text-fg-secondary hover:bg-surface-2 hover:text-fg",
        outline: "border border-eve-border bg-transparent text-fg hover:bg-surface-2",
        /* Destructive actions only — cancelling an EVE order, deleting a
           project. Never for "close this dialog". */
        danger: "bg-transparent border border-loss/50 text-loss hover:bg-loss/10",
      },
      size: {
        sm: "h-6 px-2 text-t-caption",
        md: "h-8 px-3 text-t-body",
        lg: "h-9 px-4 text-t-body",
        icon: "h-8 w-8",
      },
    },
    defaultVariants: { variant: "secondary", size: "md" },
  },
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean;
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : "button";
    return <Comp ref={ref} className={cn(buttonVariants({ variant, size }), className)} {...props} />;
  },
);
Button.displayName = "Button";

export { buttonVariants };
