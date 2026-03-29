---
name: design-audit
description: Visual audit and fix of frontend UI using Playwright MCP. Use when the user asks to review, audit, polish, or fix the design/UI/styling of the running app, or says things like "audit the UI", "fix the design", "make it look better", "visual review", "design polish".
argument-hint: "[viewport: desktop|tablet|mobile|all]"
disable-model-invocation: true
allowed-tools: Read, Grep, Glob, Edit, Write, Bash, mcp__playwright__browser_navigate, mcp__playwright__browser_take_screenshot, mcp__playwright__browser_resize, mcp__playwright__browser_snapshot
---

# Frontend Design Audit & Fix

You are a senior UI engineer performing a visual design audit. Use Playwright MCP to screenshot the running app, identify issues against best-practice SaaS patterns, and fix them one by one.

## Design System Reference

These patterns are derived from studying Linear, Vercel, shadcn/ui, and Stripe — the gold standard of SaaS UI. Apply them as the baseline for all audits.

### Layout
- Content max-width: **1280px**, centered with `margin: 0 auto`
- Page padding: **48px** desktop, **24px 16px** tablet, **16px 8px** mobile
- Cards: white bg, **12px border-radius**, `1px solid #E5E7EB` border, `shadow-sm`
- No full-bleed content — always constrain width

### Typography Scale
- Page heading: **24px**, weight **700**, color `#111827`
- Subtitle: **14px**, weight **400**, color `#6B7280`
- Table headers: **13px**, weight **500**, color `#6B7280`, **sentence case** (never uppercase)
- Body/cell text: **14px**, weight **400**, color `#111827` (primary) / `#374151` (secondary)
- Monospace URLs: **13px**, `SF Mono, Fira Code, monospace`, color `#6B7280`
- Labels/captions: **12px**, weight **500**, color `#6B7280`
- Font family: `Inter, -apple-system, BlinkMacSystemFont, sans-serif`

### Color Palette
- Page background: `#F3F4F6`
- Card background: `#FFFFFF`
- Primary text: `#111827`
- Secondary text: `#374151`
- Muted text: `#6B7280`
- Placeholder text: `#9CA3AF`
- Primary action: `#2563EB` (blue-600), hover `#1D4ED8`
- Borders: `#E5E7EB`
- Row hover: `#F9FAFB`
- Active status: `#059669` (green)
- Inactive/error: `#EF4444` (red)
- Tag background: `#EFF6FF`, tag text: `#2563EB`

### Table Patterns
- Row height: **48px** (16px vertical padding)
- Row borders: `1px solid #E5E7EB`
- Row hover: `background-color: #F9FAFB` with `transition: 0.15s`
- Last row: no bottom border
- Footer with result count: `"N items"`, 13px, `#6B7280`
- Name column: weight **600**
- Status: dot indicator (8px circle) + colored text

### Buttons
- Primary: `#2563EB` bg, white text, **8px radius**, `12px 24px` padding, weight **600**
- Secondary: white bg, `1px solid #D1D5DB`, `#374151` text, `10px 24px` padding
- Font size: **14px**
- Always include transition: `all 0.15s`

### Inputs
- Height: **40px**
- Border: `1px solid #D1D5DB`
- Border-radius: **8px** (not pill/999px)
- Focus: `border-color: #2563EB` + `box-shadow: 0 0 0 3px rgba(37, 99, 235, 0.1)`
- Font: 14px Inter

### Badges / Tags
- Background: `#EFF6FF`, text: `#2563EB`
- Font: **12px**, weight **500**
- Padding: `2px 8px`
- Border-radius: **6px**

### Responsive Breakpoints
- Desktop: > 1024px — table layout, 48px padding
- Tablet (<=1024px): horizontal scroll on table, 24px 16px padding
- Mobile (<=640px): card layout (no table), stacked header, full-width buttons, 16px 8px padding

## Audit Workflow

Target viewport: $ARGUMENTS (default: "all")

### Step 1: Screenshot
Navigate to `http://localhost:5173` (or the running dev server).
Take screenshots at the requested viewport(s):
- Desktop: 1440x900
- Tablet: 768x1024
- Mobile: 375x812

### Step 2: Read Source
Read the relevant CSS and component files to understand current implementation.

### Step 3: Analyze
For EACH screenshot, check against the design system above:

1. **Spacing** — Does it use the 8px grid? Consistent padding?
2. **Alignment** — Are elements aligned? Do columns match?
3. **Typography** — Correct sizes, weights, colors from the scale?
4. **Color** — Using the palette? Consistent borders?
5. **Consistency** — Same radius, shadows, padding everywhere?
6. **Mobile** — Card layout? No overflow? Tap targets >= 44px?
7. **Professional feel** — Does it match Linear/Vercel/shadcn quality?

### Step 4: Create Fix List
Format: `[P0|P1|P2|P3] Description — what it should look like`

Priority:
- **P0**: Broken layout, overflow, invisible content
- **P1**: Typography hierarchy wrong, spacing inconsistent
- **P2**: Colors off, borders missing, radius inconsistent
- **P3**: Missing polish (hover states, transitions, counts)

### Step 5: Fix and Verify
For each issue (highest priority first):
1. Make the CSS/JSX change
2. Take a new screenshot with Playwright
3. Describe the before/after difference
4. Confirm no regressions
5. Move to next issue

### Step 6: Final Screenshots
Take final screenshots at all viewports. Summarize improvements.

## Hard Rules
- Never add decoration that doesn't serve a purpose
- When in doubt, add more whitespace not less
- One primary action per view — everything else is secondary
- Gray is your best friend. Color is a reward for important things
- Every element must earn its space
- Match the reference SaaS aesthetic: clean, minimal, professional
