// The finished sections. A page imports them from '../blocks' and only brings the words;
// the look (web/src/look.ts) decides how each one looks and moves.
export { Header } from './Header'
export { Footer } from './Footer'
export { Hero } from './Hero'
export { PageIntro } from './PageIntro'
export { EventHero } from './EventHero'
export { Features } from './Features'
export { Steps } from './Steps'
export { Statement } from './Statement'
export { InfoTable } from './InfoTable'
export { FAQ } from './FAQ'
export { ContactSection } from './ContactSection'
export { CallToAction } from './CallToAction'
export { Gallery } from './Gallery'
export { Section, SectionHeading, Actions, buttonClass, container, cx } from './ui'
// The kit's pieces too, so '../blocks' alone is enough for a page.
export * from '../kit'
export type { Action, Item, Tone } from './types'
