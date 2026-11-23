import type { ReactNode } from 'react'

/**
 * How a look shapes the finished sections. The builder writes it into web/src/look.ts
 * from the chosen look; the sections read it, so a page only brings the content.
 */
export type Look = {
  /** split: name left, links right. centered: name centred, links below. bar: a heavy bar with a call button. announce: a coloured line above. topbar: phone and e-mail in a thin bar above. */
  header: 'split' | 'centered' | 'bar' | 'announce' | 'topbar'
  /** The opening part of the home page. */
  hero: 'editorial' | 'calm' | 'split' | 'poster' | 'pop' | 'panel' | 'framed'
  /** The opening part of an invitation or event. */
  event: 'arch' | 'band' | 'plain'
  /** How lists of services, products or menu items are shown. */
  list: 'rows' | 'cards' | 'boxes' | 'bento' | 'columns' | 'tiles'
  steps: 'row' | 'timeline'
  statement: 'quote' | 'band'
  footer: 'columns' | 'centered' | 'dark' | 'row'
  button: 'square' | 'soft' | 'pill' | 'link'
  /** The edge of cards and panels. */
  frame: 'line' | 'bold' | 'dashed' | 'double' | 'none'
  /** Headings in capital letters. */
  caps: boolean
  /** Headings and their short introduction centred. */
  centered: boolean
  /** A short thick line in the accent colour above each heading. */
  rule: boolean
  /** Cards turn a little, like stickers (wide screens only). */
  tilt: boolean
  /** More room above and below each section. */
  airy: boolean
}

/** A button or link of a section. Give `to` for a page of this site, `href` for another address, or `whatsapp` for the site's WhatsApp. */
export type Action = {
  label: string
  to?: string
  href?: string
  /** Opens the site's WhatsApp with this message ('' for none). Left out when the site has no WhatsApp number. */
  whatsapp?: string
  /** primary is the filled button; secondary is quieter. The first action is primary when left out. */
  kind?: 'primary' | 'secondary'
}

/** One item of a list of services, products, menu items or reasons. */
export type Item = {
  title: string
  text?: string
  /** A short extra on the right or as a label: a price the owner gave, a size, "Baru" (only when true). */
  note?: string
  /** A lucide-react icon element, e.g. <Wrench className="size-6" />. */
  icon?: ReactNode
  /** The product's illustration: productImage('<product name>') from '../site'. Nothing is shown when it is undefined. */
  image?: { src: string; alt: string }
}

/** The page sections can sit on: the page colour, the surface colour, the main colour or the dark text colour. */
export type Tone = 'auto' | 'plain' | 'surface' | 'primary' | 'dark'
