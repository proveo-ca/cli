# Model benchmark sources

The selected memory-range presets, read 2026-11-20 → 2026-11-21. Each preset's chart bar is its official base
model's published score; the abliterated builds publish none.

## Colour legend

| Colour | Range | Hex |
|---|---|---|
| 🟪 | pico, 6–8 GB | #8b5cf6 |
| 🟦 | nano, 8–24 GB | #0a84ff |
| 🟩 | mini, 24–96 GB | #22c55e |
| 🟧 | small, 96–256 GB (its presets also serve larger hosts) | #f59e0b |
| 🟨 | medium, 256–512 GB (reference, no preset) | #eab308 |
| 🟥 | large, 512 GB+ (reference, no preset) | #ef4444 |

## Cards

| Range | Preset (origin) | Official card |
|---|---|---|
| small | Ripple-65B Preview (non-CN) | https://huggingface.co/ricursiveAI/Ripple-65B-Preview |
| small | Qwen3.8-Flash-Next (CN) | https://huggingface.co/Qwen/Qwen3.8-Flash-Next |
| mini | Inkling 2 Mini (non-CN) | https://huggingface.co/thinkingmachines/Inkling-2-Mini |
| mini | Qwen 3.8 27B (CN) | https://huggingface.co/Qwen/Qwen3.8-27B |
| nano | Nemotron 3.5 Bolt E4B (non-CN) | https://huggingface.co/nvidia/NVIDIA-Nemotron-3.5-Bolt-E4B-BF16 |
| nano | MiMo-V2.6-Distill-Qwen-9B (CN) | https://huggingface.co/XiaomiMiMo/MiMo-V2.6-Distill-Qwen-9B |
| pico | LFM3-1.7B (non-CN) | https://huggingface.co/LiquidAI/LFM3-1.7B |
| pico | MiniCPM5-2B (CN) | https://huggingface.co/openbmb/MiniCPM5-2B |

## Cloud-class references (Terminal-Bench 2.1 chart only)

Picked by memory class first (owner: "Ignore their semantics, guide yourself by mem requirements"), then by Vals'
Terminal-Bench 2.1 board (Terminus 2), the one source that scores all four with one harness. Closed models are sized
from community estimates at FP8 (~1 byte/param), the same convention for Gemini, Opus and Luna. Vals froze the board on
2026-10-01 when Terminal-Bench 4.0 replaced it. The other charts carry no references, because these vendors publish no
SWE-bench Verified or IFBench scores.

| Class | Model (origin) | Memory basis | TB 2.1 | Sources |
|---|---|---|---|---|
| medium | Gemini 3.8 Flash (Google, US) | [Speculation] ~250–300B params (Gemini 3.5 Flash estimate), ~250–300 GB at FP8 | 81.27 | https://deepmind.google/models/model-cards/gemini-3-8-flash/ · https://gigazine.net/gsc_news/en/20260520-gemini-3-5-flash-parameter |
| medium | DeepSeek V4.1 Flash (CN) | FP4/FP8 checkpoint 510.3 GB (552B/16B), counted by weights | 74.53 | https://huggingface.co/deepseek-ai/DeepSeek-V4.1-Flash |
| large | Claude Opus 5.5 (US) | [Speculation] Opus-class estimates 0.6–7.2T params, so ≥512 GB at FP8 | 87.64 | https://www.anthropic.com/news/claude-opus-5-5 · https://eigenigma.io/en/articles/estimating-parameter-counts-of-claude-5-and-gpt-5-6/ |
| large | Kimi K3 (CN) | official checkpoint 1,560.9 GB (2.8T/104B) | 80.90 | https://huggingface.co/moonshotai/Kimi-K3 |

Board: https://vals.ai/benchmarks/terminal-bench-2-1. Medium moved to 256–512 GB when small became 96–256 GB
(owner, 2026-11-21). MiMo V2.6 Flash (177.7 GB) now falls in small's range and left the chart. Gemini 3.8 Flash first
looked small because its source quotes ~110–150 GB at ~4-bit; at FP8, like the other closed models, it is medium.
Classes count checkpoint weights (owner: "precision can be low"), so DeepSeek V4.1 Flash at 510.3 GB is medium even
though its KV cache would take it past 512 GB. Chinese medium runners-up: GLM-5.3-Flash (FP8 328.3 GB, 62.92 on Vals;
84.3 in Z.ai's Claude Code harness) and MiniMax M3 (MXFP8 443.7 GB, 53.56). Non-Chinese medium runners-up: Beam 1.0 (Reflection, NVFP4 293.6 GB, 70.8 in Ricursive's Terminus 2 run, not on Vals)
and Nemotron 3 Ultra (NVFP4 352.3 GB, 50.94 on Vals).
GPT-6 Luna's estimates (740–1,200 GB at FP8) put it in large.

## Charts

| Chart | Presets | Missing |
|---|---|---|
| terminal-bench-2.1.mermaid (agentic) | 7/8 | Bolt E4B |
| swe-bench-verified.mermaid (agentic coding) | 7/8 | Bolt E4B |
| ifbench.mermaid (instruction following) | 6/8 | Inkling 2 Mini, MiMo-V2.6 |

No benchmark covers all 8 presets, from a vendor or a third party (checked 2026-11-21, incl. the Artificial
Analysis Intelligence Index v5.0.1 at 5/8). Bolt's card has no coding or agentic rows, so IFBench is the chart that
includes it; together the three charts show every preset.

† A score from another vendor's card, used only where the model's own card has none: Flash-Next's Terminal-Bench 2.1
(85.80) and SWE-bench Verified (78.7) from NVIDIA's Nemotron 3.5 Super card (https://huggingface.co/nvidia/NVIDIA-Nemotron-3.5-Super-VL-120B-A12B-BF16), and Qwen 3.8 27B's SWE-bench Verified
(77.8) from the Inkling 2 Mini card.

Left out: τ2-bench (2/8), LiveCodeBench v6 and GPQA Diamond (5/8, not agentic), BFCL (V3 and V4 mix).
