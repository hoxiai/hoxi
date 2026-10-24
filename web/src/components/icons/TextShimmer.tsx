import { createEffect, createMemo, on, onCleanup, onMount, type ValidComponent } from "solid-js"
import { createStore } from "solid-js/store"
import { Dynamic } from "solid-js/web"
import { observeSpinner } from "./Spinner"

export const TextShimmer = <T extends ValidComponent = "span">(props: {
  text: string
  class?: string
  as?: T
  active?: boolean
  offset?: number
}) => {
  const active = createMemo(() => props.active ?? true)
  const offset = createMemo(() => props.offset ?? 0)

  return (
    <Dynamic
      component={props.as || "span"}
      ref={(el: Element) => {
        if (el instanceof Element) observeSpinner(el)
      }}
      data-component="text-shimmer"
      data-active={active() ? "true" : "false"}
      class={props.class}
      aria-label={props.text}
      style={{
        "--text-shimmer-swap": "220ms",
        "--text-shimmer-index": `${offset()}`,
      }}
    >
      <span data-slot="text-shimmer-char" aria-hidden="true">
        <span data-slot="text-shimmer-char-base">{props.text}</span>
        <span data-slot="text-shimmer-char-shimmer">{props.text}</span>
      </span>
    </Dynamic>
  )
}

/**
 * Kilo's ToolStatusTitle — smoothly morphs between active (shimmering) and done text
 * while smoothly animating width changes.
 */
export function ToolStatusTitle(props: {
  active: boolean
  activeText: string
  doneText: string
  split?: boolean
  class?: string
}) {
  const split = () => props.split ?? true

  const parts = createMemo(() => {
    if (!split()) return undefined
    const activeWords = props.activeText.split(" ")
    const doneWords = props.doneText.split(" ")
    let i = 0
    while (
      i < activeWords.length - 1 &&
      i < doneWords.length - 1 &&
      activeWords[activeWords.length - 1 - i] === doneWords[doneWords.length - 1 - i]
    ) {
      i++
    }
    if (i === 0) return undefined
    return {
      activePrefix: activeWords.slice(0, activeWords.length - i).join(" "),
      donePrefix: doneWords.slice(0, doneWords.length - i).join(" "),
      suffix: " " + activeWords.slice(activeWords.length - i).join(" "),
    }
  })

  return (
    <span class={`inline-flex items-baseline ${props.class ?? ""}`}>
      {parts() ? (
        <>
          <TextShimmer text={props.active ? parts()!.activePrefix : parts()!.donePrefix} active={props.active} />
          <TextShimmer text={parts()!.suffix} active={props.active} offset={parts()!.activePrefix.length} />
        </>
      ) : (
        <TextShimmer text={props.active ? props.activeText : props.doneText} active={props.active} />
      )}
    </span>
  )
}

const px = (value: number | string | undefined, fallback: number) => {
  if (typeof value === "number") return `${value}px`
  if (typeof value === "string") return value
  return `${fallback}px`
}

const ms = (value: number | string | undefined, fallback: number) => {
  if (typeof value === "number") return `${value}ms`
  if (typeof value === "string") return value
  return `${fallback}ms`
}

const pct = (value: number | undefined, fallback: number) => {
  const v = value ?? fallback
  return `${v}%`
}

/**
 * Kilo's TextReveal — top-to-bottom CSS gradient mask wipe animation for live
 * step details and reasoning summaries without vertical height changes.
 */
export function TextReveal(props: {
  text?: string
  class?: string
  duration?: number | string
  edge?: number
  travel?: number | string
  spring?: string
  springSoft?: string
  growOnly?: boolean
  truncate?: boolean
}) {
  const [state, setState] = createStore({
    cur: props.text,
    old: undefined as string | undefined,
    width: "auto",
    ready: false,
    swapping: false,
  })
  let inRef: HTMLSpanElement | undefined
  let outRef: HTMLSpanElement | undefined
  let rootRef: HTMLSpanElement | undefined
  let frame: number | undefined

  const win = () => inRef?.scrollWidth ?? 0
  const wout = () => outRef?.scrollWidth ?? 0

  const widen = (next: number) => {
    if (props.truncate || next <= 0) return
    if (props.growOnly ?? true) {
      const prev = Number.parseFloat(state.width)
      if (Number.isFinite(prev) && next <= prev) return
    }
    setState("width", `${next}px`)
  }

  createEffect(
    on(
      () => props.text,
      (next, prev) => {
        if (next === prev) return
        if (typeof next === "string" && typeof prev === "string" && next.startsWith(prev)) {
          setState("cur", next)
          if (!props.truncate) widen(win())
          return
        }
        setState("swapping", true)
        setState("old", prev)
        setState("cur", next)

        if (typeof requestAnimationFrame !== "function") {
          if (!props.truncate) widen(Math.max(win(), wout()))
          void rootRef?.offsetHeight
          setState("swapping", false)
          return
        }
        if (frame !== undefined && typeof cancelAnimationFrame === "function") cancelAnimationFrame(frame)
        frame = requestAnimationFrame(() => {
          if (!props.truncate) widen(Math.max(win(), wout()))
          void rootRef?.offsetHeight
          setState("swapping", false)
          frame = undefined
        })
      },
    ),
  )

  onMount(() => {
    if (!props.truncate) widen(win())
    const fonts = typeof document !== "undefined" ? document.fonts : undefined
    if (typeof requestAnimationFrame !== "function") {
      setState("ready", true)
      return
    }
    if (!fonts) {
      requestAnimationFrame(() => setState("ready", true))
      return
    }
    void fonts.ready.finally(() => {
      if (!props.truncate) widen(win())
      requestAnimationFrame(() => setState("ready", true))
    })
  })

  onCleanup(() => {
    if (frame === undefined || typeof cancelAnimationFrame !== "function") return
    cancelAnimationFrame(frame)
  })

  return (
    <span
      ref={rootRef}
      data-component="text-reveal"
      data-ready={state.ready ? "true" : "false"}
      data-swapping={state.swapping ? "true" : "false"}
      data-truncate={props.truncate ? "true" : "false"}
      class={props.class}
      aria-label={props.text ?? ""}
      style={{
        "--text-reveal-duration": ms(props.duration, 380),
        "--text-reveal-edge": pct(props.edge, 17),
        "--text-reveal-travel": px(props.travel, 2),
        "--text-reveal-spring": props.spring ?? "cubic-bezier(0.34, 1.08, 0.64, 1)",
        "--text-reveal-spring-soft": props.springSoft ?? "cubic-bezier(0.34, 1, 0.64, 1)",
      }}
    >
      <span data-slot="text-reveal-track" style={{ width: props.truncate ? "100%" : state.width }}>
        <span data-slot="text-reveal-entering" ref={inRef}>
          {state.cur ?? "\u00A0"}
        </span>
        <span data-slot="text-reveal-leaving" ref={outRef}>
          {state.old ?? "\u00A0"}
        </span>
      </span>
    </span>
  )
}
