import { createMemo, type ValidComponent } from "solid-js"
import { Dynamic } from "solid-js/web"

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
