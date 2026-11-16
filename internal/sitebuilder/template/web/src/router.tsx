import { useEffect, useState, type AnchorHTMLAttributes, type MouseEvent } from 'react'

const EVENT = 'site:navigate'

/** The path of the page being shown. */
export function usePath(): string {
  const [path, setPath] = useState(() => window.location.pathname)
  useEffect(() => {
    const update = () => setPath(window.location.pathname)
    window.addEventListener('popstate', update)
    window.addEventListener(EVENT, update)
    return () => {
      window.removeEventListener('popstate', update)
      window.removeEventListener(EVENT, update)
    }
  }, [])
  return path
}

/** Goes to another page of this site. */
export function navigate(to: string) {
  if (to !== window.location.pathname) window.history.pushState({}, '', to)
  window.dispatchEvent(new Event(EVENT))
  window.scrollTo(0, 0)
}

type LinkProps = AnchorHTMLAttributes<HTMLAnchorElement> & { to: string }

/** A link. Pages of this site open without reloading; other addresses work as usual. */
export function Link({ to, onClick, ...rest }: LinkProps) {
  const external = /^(https?:|mailto:|tel:|#)/.test(to)
  const handle = (event: MouseEvent<HTMLAnchorElement>) => {
    onClick?.(event)
    if (external || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    navigate(to)
  }
  return <a href={to} onClick={handle} {...(external && /^https?:/.test(to) ? { target: '_blank', rel: 'noreferrer' } : {})} {...rest} />
}
