import type { CSSProperties } from "react";
import { achievementGlyphSvgById, type AchievementIconId } from "@/assets/achievements/achievement-icons";
import { cn } from "@/lib/utils";
import "./AchievementBadge.css";

export type AchievementRarity = "bronze" | "silver" | "gold" | "platinum" | "classified";
export type AchievementState = "locked" | "active" | "unlocked";
export type AchievementBadgeSize = "sm" | "md" | "lg";

interface AchievementBadgeProps {
  iconId: AchievementIconId;
  rarity?: AchievementRarity;
  state?: AchievementState;
  size?: AchievementBadgeSize;
  progress?: number;
  label?: string;
  glyphScale?: number;
  glyphOffsetX?: number;
  glyphOffsetY?: number;
  className?: string;
}

type AchievementBadgeStyle = CSSProperties & {
  "--achievement-progress"?: string;
  "--achievement-glyph-scale"?: number;
  "--achievement-glyph-offset-x"?: string;
  "--achievement-glyph-offset-y"?: string;
};

export function AchievementBadge({
  iconId,
  rarity = "bronze",
  state = "locked",
  size = "md",
  progress,
  label,
  glyphScale = 1,
  glyphOffsetX = 0,
  glyphOffsetY = 0,
  className,
}: AchievementBadgeProps) {
  const glyphSvg = achievementGlyphSvgById[iconId];
  const normalizedProgress = Math.max(0, Math.min(100, progress ?? (state === "unlocked" ? 100 : 0)));
  const title = label ?? iconId.replace(/-/g, " ");
  const style: AchievementBadgeStyle = {
    "--achievement-progress": `${normalizedProgress}%`,
    "--achievement-glyph-scale": glyphScale,
    "--achievement-glyph-offset-x": `${glyphOffsetX}%`,
    "--achievement-glyph-offset-y": `${glyphOffsetY}%`,
  };

  return (
    <span
      className={cn(
        "achievement-badge",
        `achievement-badge--${rarity}`,
        `achievement-badge--${state}`,
        `achievement-badge--${size}`,
        className,
      )}
      title={title}
      aria-label={title}
      role="img"
      style={style}
    >
      <span className="achievement-badge__hex achievement-badge__hex--outer" aria-hidden="true" />
      <span className="achievement-badge__hex achievement-badge__hex--inner" aria-hidden="true" />
      <span className="achievement-badge__progress" aria-hidden="true" />
      <span className="achievement-badge__ring" aria-hidden="true" />
      <span className="achievement-badge__glyph" aria-hidden="true" dangerouslySetInnerHTML={{ __html: glyphSvg }} />
      <span className="achievement-badge__tick achievement-badge__tick--top" aria-hidden="true" />
      <span className="achievement-badge__tick achievement-badge__tick--bottom" aria-hidden="true" />
    </span>
  );
}
