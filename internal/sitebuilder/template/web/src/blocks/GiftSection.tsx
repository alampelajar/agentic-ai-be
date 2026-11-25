import { Check, Copy, Gift, MapPin } from 'lucide-react'
import { useState } from 'react'
import { look } from '../look'
import { site } from '../site'
import { Stagger } from '../kit/Stagger'
import { slug } from './Features'
import type { Tone } from './types'
import { Section, SectionHeading, buttonClass, cx, frame } from './ui'

type Account = {
  /** The bank or wallet, e.g. "BCA" or "GoPay". */
  bank: string
  /** The account number exactly as the brief gives it. */
  number: string
  /** The name on the account. */
  holder?: string
}

type GiftSectionProps = {
  /** The heading, e.g. "Tanda kasih". */
  title: string
  /** One kind sentence, e.g. that a gift is welcome but not expected. */
  intro?: string
  /** Accounts for a gift by transfer, only the ones the brief gives. */
  accounts?: Account[]
  /** Where a gift can be sent, only when the brief gives it. */
  address?: string
  tone?: Tone
  id?: string
}

const words = {
  id: { copy: 'Salin', copied: 'Tersalin', holder: 'atas nama', send: 'Kirim hadiah ke' },
  en: { copy: 'Copy', copied: 'Copied', holder: 'in the name of', send: 'Send a gift to' },
}

function CopyButton({ value, tone }: { value: string; tone: Tone }) {
  const t = words[site.language] ?? words.id
  const [copied, setCopied] = useState(false)
  const copy = () => {
    navigator.clipboard
      ?.writeText(value)
      .then(() => {
        setCopied(true)
        window.setTimeout(() => setCopied(false), 2000)
      })
      .catch(() => setCopied(false))
  }
  return (
    <button type='button' onClick={copy} className={cx(buttonClass('secondary', tone), 'min-h-10 px-4 py-2 text-sm')} aria-live='polite'>
      {copied ? <Check className='size-4' aria-hidden='true' /> : <Copy className='size-4' aria-hidden='true' />}
      {copied ? t.copied : t.copy}
    </button>
  )
}

/** Gifts for a wedding or celebration: bank or wallet accounts with a copy button, and an address for parcels. Shows nothing when neither is given. */
export function GiftSection({ title, intro, accounts, address, tone = 'auto', id }: GiftSectionProps) {
  const t = words[site.language] ?? words.id
  const list = (accounts ?? []).filter((account) => account && account.bank && account.number).slice(0, 4)
  if (list.length === 0 && !address) return null
  const headingId = `${id ?? slug(title)}-title`
  const shape = look.button === 'pill' ? 'rounded-3xl' : look.button === 'square' ? 'rounded-none' : 'rounded-xl'

  return (
    <Section tone={tone} id={id} labelledBy={headingId}>
      <SectionHeading id={headingId} title={title} intro={intro} tone={tone} center />
      <Stagger className={cx('mx-auto grid max-w-4xl gap-6', list.length + (address ? 1 : 0) > 1 && 'md:grid-cols-2')}>
        {list.map((account) => (
          <article key={account.bank + account.number} className={cx('flex h-full flex-col items-center gap-4 bg-surface p-8 text-center text-foreground', shape, frame() || 'border border-border')}>
            <Gift className='size-7 text-accent' aria-hidden='true' />
            <h3 className={cx('font-heading text-2xl', look.caps && 'uppercase tracking-wide')}>{account.bank}</h3>
            <p className='font-heading text-2xl tracking-wider tabular-nums md:text-3xl'>{account.number}</p>
            {account.holder ? (
              <p className='text-muted'>
                {t.holder} {account.holder}
              </p>
            ) : null}
            <div className='mt-auto pt-2'>
              <CopyButton value={account.number.replace(/\s+/g, '')} tone='plain' />
            </div>
          </article>
        ))}
        {address ? (
          <article className={cx('flex h-full flex-col items-center gap-4 bg-surface p-8 text-center text-foreground', shape, frame() || 'border border-border')}>
            <MapPin className='size-7 text-accent' aria-hidden='true' />
            <h3 className={cx('font-heading text-2xl', look.caps && 'uppercase tracking-wide')}>{t.send}</h3>
            <p className='max-w-xs leading-relaxed text-muted'>{address}</p>
            <div className='mt-auto pt-2'>
              <CopyButton value={address} tone='plain' />
            </div>
          </article>
        ) : null}
      </Stagger>
    </Section>
  )
}

export default GiftSection
