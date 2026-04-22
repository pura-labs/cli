---
theme: mono
aspect: "16/9"
title: "Weekly · Week 47"
---

# Weekly
Week 47 · Platform team

<!-- notes: 10 minute slot. Skip slide 4 if Q&A runs long. -->

---

# Highlights

<span class="eyebrow">Done</span>
- Tool dispatcher ships
- 4 chef themes live
- Inbox approval badge

<span class="eyebrow">Cut</span>
- PPTX export (moved to P2)

---

# Metrics

<div class="pullquote">
  Latency median 180ms → 120ms after the edge-cache fix
</div>

- Build → deploy under 4 min
- p99 SLO holding at 99.2%

---

# Risks

- Session cookie drift on multi-tab edits
- * Mitigation: CAS with soft 409 surfacing
- * Fallback: optimistic merge via patch_section

---

# Next week
- Theme gallery UI
- SPA live preview for slides
- Book → slides compose POC

<span class="footnote">Owners in Linear · channel #platform-weekly</span>
