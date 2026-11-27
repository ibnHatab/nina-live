# Diagrams: how Nina reasons and improves (2026-11-27)

Graphviz sources and their SVG renders, read from the code (nina-live, acasa, nina-narratives):

| file | shows |
|---|---|
| `dataflow.dot` | data flow 1: Gemini Live, pi, the Metis composition and the semantic world coupled through the event log (verbs down; outcomes, beliefs, Steer/FollowUp up; MCP) |
| `twolevel.dot` | the two-level reasoning system: what crosses between the LLMs (level 1) and the semantic world + Metis rules (level 2) |
| `improvement.dot` | data flow 2: surprises → workspace → metis-loop (case, pack, gates, ruling) → approval → catalog; what produces today and what is planned |
| `evolution.dot` | how the design evolved from the Three Streams paper |
| `streamlined.dot` | proposal: data flow 1 folded into three channels around the director's two cards (intent port, view port, MCP as a measured residual); to be tried after the real SITL loop gives metrics |

Colours carry meaning: `#0000aa` the coupling under discussion, `#888888` dashed = planned or idle. A page
that embeds the SVGs maps them to its theme (the Nina Reasoning Map artifact does).

    for f in docs/diagrams/*.dot; do dot -Tsvg "$f" -o "${f%.dot}.svg"; done
