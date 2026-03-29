---
name: fe-dev
description: Frontend development following the project design system. Auto-triggers when creating or modifying React components, CSS styles, or UI elements in this project. Ensures all new UI code matches the established design tokens and patterns.
user-invocable: false
allowed-tools: Read, Grep, Glob, Edit, Write
paths: "src/**/*.jsx,src/**/*.tsx,src/**/*.css"
---

# Frontend Development — Design System Guide

When writing or modifying any frontend code in this project, follow these design tokens and patterns. They are derived from a formal audit against Linear, Vercel, and shadcn/ui.

## Tech Stack
- React (JSX) with Vite
- Plain CSS (no Tailwind, no CSS modules)
- Styles in `src/styles.css`
- Components in `src/components/`

## Design Tokens

### Colors
```
--bg-page:        #F3F4F6    /* page background */
--bg-card:        #FFFFFF    /* card/surface background */
--bg-hover:       #F9FAFB    /* row/item hover */
--bg-tag:         #EFF6FF    /* tag/badge background */

--text-primary:   #111827    /* headings, names, important text */
--text-secondary: #374151    /* body text, owner names */
--text-muted:     #6B7280    /* labels, captions, table headers, URLs */
--text-placeholder:#9CA3AF   /* input placeholders */

--color-primary:  #2563EB    /* primary buttons, links, active states */
--color-primary-hover: #1D4ED8
--color-success:  #059669    /* active/success status */
--color-danger:   #EF4444    /* inactive/error status */

--border-default: #E5E7EB    /* borders, dividers */
--border-input:   #D1D5DB    /* input borders */

--focus-ring:     rgba(37, 99, 235, 0.1)  /* focus box-shadow */
```

### Typography
```
Font:      Inter, -apple-system, BlinkMacSystemFont, sans-serif
Mono:      SF Mono, Fira Code, Fira Mono, Roboto Mono, monospace

Heading:   24px / 700 / #111827
Subtitle:  14px / 400 / #6B7280
Body:      14px / 400 / #111827 or #374151
Label:     12-13px / 500 / #6B7280
URL/Code:  13px / 400 / #6B7280 (monospace)
Tag:       12px / 500 / #2563EB
```

### Spacing (8px grid)
```
Page padding:     48px (desktop), 24px 16px (tablet), 16px 8px (mobile)
Card padding:     24px
Section gap:      24px
Element gap:      8-16px
Input height:     40px
Row padding:      16px 24px (vertical horizontal)
```

### Radius
```
Cards:   12px
Buttons: 8px
Inputs:  8px
Tags:    6px
```

### Shadows
```
Cards: 0 1px 2px 0 rgba(0, 0, 0, 0.05)  /* always paired with 1px border */
```

## Component Patterns

### New Components
When creating a new component:
1. Create `src/components/ComponentName.jsx`
2. Add styles to `src/styles.css` (not inline, not CSS modules)
3. Use semantic class names: `.card-header`, `.cell-name`, `.btn-primary`
4. Always add `transition: all 0.15s` or `transition: background-color 0.15s` to interactive elements

### Tables
- Use `<table>` with class `table`
- Headers: sentence case ("Name", not "NAME"), 13px, weight 500, #6B7280
- Rows: 1px #E5E7EB border, hover #F9FAFB
- Name cells: weight 600
- URL cells: monospace, 13px, #6B7280
- Always include a footer with item count

### Buttons
- Primary: `.btn .btn-primary` — blue filled
- Secondary: `.btn .btn-secondary` — white with border
- One primary action per view, top-right of header
- 14px, weight 600, 8px radius

### Forms
- Labels: 14px, weight 500, #374151
- Inputs: 40px height, 8px radius, 1px #D1D5DB border
- Focus: blue border + ring shadow
- Stacked fields with 24px gap

### Status Indicators
- Dot (8px circle) + colored text
- Active: green (#059669)
- Inactive: red (#EF4444)

### Tags/Badges
- Light blue bg (#EFF6FF) + blue text (#2563EB)
- 12px, weight 500, 2px 8px padding, 6px radius
- Wrap with `flex-wrap: wrap` and `gap: 6px`

## Responsive Rules

### Mobile (<=640px)
- Tables become card layouts (`.mobile-cards`)
- Header stacks vertically, buttons go full-width
- Search and filters stack vertically
- Card padding reduces to 16px

### Tablet (<=1024px)
- Table gets `overflow-x: auto` with `min-width: 700px`
- Page padding reduces to 24px 16px

## Anti-Patterns (Do NOT)
- Do not use `border-radius: 999px` on inputs (only on filter chips)
- Do not use `text-transform: uppercase` on table headers
- Do not use inline styles for anything that should be a reusable class
- Do not use `#000000` for text — use `#111827`
- Do not use shadows without borders on cards
- Do not create full-bleed layouts — always constrain to `max-width: 1280px`
- Do not add decorative elements that don't serve a purpose
- Do not use more than 2-3 colors on a page — gray is primary, blue is accent
