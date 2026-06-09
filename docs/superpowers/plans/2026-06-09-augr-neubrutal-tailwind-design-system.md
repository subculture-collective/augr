# Augr Neubrutal Tailwind Design System Implementation Plan

> **For agentic workers:** Execute this plan task-by-task. Recommended path:
> dispatch a fresh subagent per task, review each result with `review-quality`,
> then continue. For complex multi-agent splits, use
> `parallel-feature-development`, `team-composition-patterns`, and
> `team-communication-protocols`. Steps use checkbox (`- [ ]`) syntax for
> tracking.

**Goal:** Recreate the `docs/design/ui-tailwind-spec.md` visual system inside Augr's existing single Vite/Tailwind v4 SPA.

**Architecture:** The spec assumes a two-SPA Tailwind v3 project, but Augr is a single React/Vite/Tailwind v4 SPA under `web/`. Implement the design as a CSS-first Tailwind v4 token layer in `web/src/index.css`, reusable HUD primitives in `web/src/components/ui/hud.tsx`, and broad app chrome/shared primitive restyling so existing pages inherit the neubrutal HUD look without adding live/order mutations.

**Tech Stack:** React, TypeScript, Vite, Tailwind CSS v4 via `@tailwindcss/vite`, `class-variance-authority`, `tailwind-merge`, Vitest/JSDOM.

---

## Spec Adaptation

- The spec's `app/` and `dashboard/` roots do not exist in this repository.
- The implementation target is `web/`, with global CSS in `web/src/index.css` and route chrome in `web/src/components/layout/app-shell.tsx`.
- The requested `tailwind.config.js` is translated into Tailwind v4 `@theme inline` variables and CSS utilities.
- Viewer-only constructs like transcript/courtroom overlays become reusable HUD primitives and CSS classes; they are not wired to nonexistent routes.
- No backend, trading, execution, or live-order code is in scope.

## File Structure

- Modify: `web/src/index.css` — design tokens, Tailwind v4 theme variables, global HUD utilities, scrollbars, reduced motion, hard-corner base reset.
- Create: `web/src/components/ui/hud.tsx` — `ConsolePanel`, `HudSection`, `StatusLed`, `HudBadge`, `TabButton`, `HudRow` primitives.
- Create: `web/src/components/ui/hud.test.tsx` — accessibility and class-contract tests for HUD primitives.
- Modify: `web/src/components/ui/button.tsx` — hard-corner, outline/shadow, signal/pulse/alert variants.
- Modify: `web/src/components/ui/badge.tsx` — HUD badge tones and square corners.
- Modify: `web/src/components/ui/card.tsx` — console-panel card surfaces.
- Modify: `web/src/components/ui/input.tsx`, `web/src/components/ui/textarea.tsx`, `web/src/components/ui/label.tsx` — HUD input/label focus and micro-label styling.
- Modify: `web/src/components/ui/dialog.tsx` — hard panel overlay treatment.
- Modify: `web/src/components/layout/page-header.tsx` — HUD section/header composition.
- Modify: `web/src/components/layout/app-shell.tsx` — control-room shell, grouped HUD nav, status bar treatment.
- Modify: `web/src/pages/dashboard-page.tsx` — dashboard landing composition using new HUD primitives.
- Modify: high-impact route pages only if the shared primitive pass leaves obvious mismatches: `web/src/pages/risk-page.tsx`, `web/src/pages/decision-journal-page.tsx`, `web/src/pages/replay-page.tsx`, `web/src/pages/options-page.tsx`, `web/src/pages/polymarket-page.tsx`.
- Test/validate: `web/src/components/layout/app-shell.test.tsx` if existing assertions need update, plus focused page tests touched by styling changes.

## Commit Strategy

1. `docs: plan neubrutal tailwind system` — commit the design spec and this plan.
2. `feat(ui): add neubrutal design tokens` — global CSS/theme/utilities and HUD primitive tests.
3. `feat(ui): restyle shared primitives` — button/card/input/badge/dialog/page-header/app-shell.
4. `feat(ui): apply hud styling to key pages` — dashboard and high-impact trading/research pages.

## Task 1: Persist the Plan

**Files:**
- Create: `docs/superpowers/plans/2026-06-09-augr-neubrutal-tailwind-design-system.md`
- Include in commit: `docs/design/ui-tailwind-spec.md`

- [ ] Run `rtk git status --short` and verify only the new spec/plan are staged for the first commit.
- [ ] Run `rtk git diff --check`.
- [ ] Commit:

```bash
rtk git add docs/design/ui-tailwind-spec.md docs/superpowers/plans/2026-06-09-augr-neubrutal-tailwind-design-system.md
rtk git commit -m "docs: plan neubrutal tailwind system"
```

## Task 2: Add Tailwind v4 Design Tokens and HUD Utilities

**Files:**
- Modify: `web/src/index.css`
- Create: `web/src/components/ui/hud.tsx`
- Create: `web/src/components/ui/hud.test.tsx`

- [ ] Replace the old blue rounded theme variables in `web/src/index.css` with the spec tokens: `void`, `panel`, `border`, `ink`, `signal`, `pulse`, `alert`, `caution`, `confirm`, `dead`, plus compatibility aliases for existing `background`, `foreground`, `card`, `primary`, `secondary`, `muted`, `accent`, `destructive`, `success`, `warning`, and `info`.
- [ ] Add Tailwind v4 `@theme inline` mappings for all spec tokens: `--color-void`, `--color-panel`, `--color-signal`, `--text-2xs`, `--text-hud`, `--animate-blink`, `--animate-slide-in-right`, `--animate-slide-in-up`, `--animate-stinger-shake`, `--animate-scan`, `--animate-pulse-slow`, and `--border-width-3`.
- [ ] Add CSS keyframes: `blink`, `slideInRight`, `slideInUp`, `stingerShake`, and `scan`.
- [ ] Add global utilities/classes from the spec: `.hud-bracket`, corner variants, `.hud-prompt`, `.hud-led`, `.hud-led-live`, `.hud-led-sync`, `.hud-led-ok`, `.hud-led-warn`, `.hud-cursor`, `.hud-scan`, `.hud-row`, `.hud-row-key`, `.hud-row-val`, `.hud-section`, `.hud-section-label`, `.hud-section-line`, `.hud-statusbar`, `.hud-panel`, `.hud-panel-raised`, `.hud-divide`, `.hud-badge`, `.hud-transcript-entry`, `.hud-transcript-meta`, `.hud-transcript-speaker`, `.hud-transcript-dialogue`, `.overlay-safe`.
- [ ] Add reduced-motion guard:

```css
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    scroll-behavior: auto !important;
    transition-duration: 0.01ms !important;
  }
}
```

- [ ] Add `web/src/components/ui/hud.tsx` exporting:
  - `ConsolePanel({ className, children, ...props })`
  - `HudSection({ label, note, className })`
  - `StatusLed({ state, label })` with states `live | sync | ok | warn | dead`
  - `HudBadge({ tone, className, children })` with tones `signal | pulse | alert | caution | confirm | dead | ink`
  - `TabButton({ active, label, note, ...buttonProps })`
  - `HudRow({ label, value, accent, className })`
- [ ] Add `web/src/components/ui/hud.test.tsx` tests that render each primitive, assert accessible labels for LEDs/tab buttons, and assert no primitive requires backend data.
- [ ] Run `rtk npm test -- --run src/components/ui/hud.test.tsx` from `web`.
- [ ] Run `rtk lint` from `web`.
- [ ] Commit:

```bash
rtk git add web/src/index.css web/src/components/ui/hud.tsx web/src/components/ui/hud.test.tsx
rtk git commit -m "feat(ui): add neubrutal design tokens"
```

## Task 3: Restyle Shared Primitives and Shell

**Files:**
- Modify: `web/src/components/ui/button.tsx`
- Modify: `web/src/components/ui/badge.tsx`
- Modify: `web/src/components/ui/card.tsx`
- Modify: `web/src/components/ui/input.tsx`
- Modify: `web/src/components/ui/textarea.tsx`
- Modify: `web/src/components/ui/label.tsx`
- Modify: `web/src/components/ui/dialog.tsx`
- Modify: `web/src/components/layout/page-header.tsx`
- Modify: `web/src/components/layout/app-shell.tsx`

- [ ] Make shared primitives hard-cornered with `rounded-none`, hard borders, no soft shadows, uppercase HUD metadata where appropriate, and `focus-visible:ring-1 focus-visible:ring-pulse`.
- [ ] Preserve existing variant names (`default`, `secondary`, `outline`, `ghost`, `destructive`, `success`, `warning`) so current pages/tests keep compiling.
- [ ] Apply app shell control-room styling: dark `void` canvas, `hud-scan` background, square bordered sidebar, uppercase group labels, signal/pulse active nav, top status bar with signed-in/guest state.
- [ ] Update `PageHeader` so `eyebrow` is rendered as a spec-style micro-label and the section frame is a `hud-panel`.
- [ ] Run focused tests likely affected by shared chrome:

```bash
rtk npm test -- --run src/components/layout/app-shell.test.tsx src/pages/dashboard-page.test.tsx
```

- [ ] Run `rtk lint` from `web`.
- [ ] Commit:

```bash
rtk git add web/src/components/ui web/src/components/layout
rtk git commit -m "feat(ui): restyle shared primitives"
```

## Task 4: Apply HUD Styling to Key Pages

**Files:**
- Modify: `web/src/pages/dashboard-page.tsx`
- Modify as needed: `web/src/pages/risk-page.tsx`, `web/src/pages/decision-journal-page.tsx`, `web/src/pages/replay-page.tsx`, `web/src/pages/options-page.tsx`, `web/src/pages/polymarket-page.tsx`

- [ ] Update the dashboard landing with a neubrutal broadcast-control hierarchy: status badges, console panels, asymmetric grid, uppercase section metadata, and no rounded card chrome beyond shared primitive inheritance.
- [ ] Update only obvious high-impact page wrappers where current inline styling fights the new primitives; avoid refactoring data logic.
- [ ] Keep all scanner, journal, replay, and risk cockpit UIs read-only where they are already read-only. Do not add mutations.
- [ ] Run focused page tests for touched pages.
- [ ] Run `rtk lint` and `rtk npm run build` from `web`.
- [ ] Commit:

```bash
rtk git add web/src/pages
rtk git commit -m "feat(ui): apply hud styling to key pages"
```

## Task 5: Final Validation and Completion Report

**Files:**
- No new files expected.

- [ ] Run final frontend validation:

```bash
rtk lint
rtk npm run build
rtk npm test -- --run
```

- [ ] Run relevant backend smoke only if frontend commits touched shared generated types or routing contracts:

```bash
rtk go test ./cmd/tradingagent -run 'Docs|Architecture|Readme|DevelopmentSetup|ProductionDockerCompose|LiveGate|LiveTrading'
```

- [ ] Run `rtk git status --short` and confirm clean after commits.
- [ ] Report commits, validation, and any remaining visual caveats.

## Acceptance Criteria

- Global CSS exposes the spec's color, typography, border, animation, HUD utility, scrollbar, and reduced-motion system in Tailwind v4 form.
- Shared UI primitives and app shell visually match “Neubrutal HUD”: flat dark panels, hard outlines, hard corners, purple/indigo accents, monospace metadata, uppercase labels, and no soft shadows/glassmorphism.
- Key dashboard/trading surfaces inherit the new system without breaking existing API behavior.
- No new backend, live trading, order placement, or mutation capabilities are introduced.
- Frontend lint/build/tests pass, with only known Vite chunk-size or jsdom navigation warnings if present.
