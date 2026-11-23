import { look } from '../look'
import { site } from '../site'
import { Reveal } from '../kit/Reveal'
import type { Action } from './types'
import { Actions, container, cx } from './ui'

type CallToActionProps = {
  /** A short invitation, e.g. "Pesan untuk acara Anda". */
  title: string
  text?: string
  /** The buttons; left out to offer WhatsApp. */
  actions?: Action[]
}

const words = { id: 'Chat lewat WhatsApp', en: 'Chat on WhatsApp' }

/** An invitation to act, near the end of a page, on a band of the main colour. */
export function CallToAction({ title, text, actions }: CallToActionProps) {
  const list = actions && actions.length ? actions : [{ label: words[site.language] ?? words.id, whatsapp: '' }]
  return (
    <section className='bg-primary text-primary-foreground'>
      <Reveal from='up' className={cx(container, 'flex flex-col gap-8 py-20 md:flex-row md:items-end md:justify-between md:py-24', look.centered && 'md:flex-col md:items-center md:text-center')}>
        <div className='max-w-2xl'>
          <h2 className={cx('font-heading text-3xl leading-tight tracking-tight md:text-5xl', look.caps && 'uppercase')}>{title}</h2>
          {text ? <p className='mt-4 text-lg opacity-85 md:text-xl'>{text}</p> : null}
        </div>
        <Actions actions={list} tone='primary' className='shrink-0' />
      </Reveal>
    </section>
  )
}

export default CallToAction
