import { MessageCircle } from 'lucide-react'
import { whatsappLink } from '../site'
import { words } from './words'

type WhatsAppButtonProps = {
  /** The message already typed for the visitor, e.g. "Halo, saya mau pesan kue." */
  message?: string
  /** The words beside the icon on wider screens. Default "Chat WhatsApp". */
  label?: string
}

/**
 * A round WhatsApp button that stays in the bottom corner of every page. Put it once
 * in Layout. It shows nothing when the site has no WhatsApp number. It appears with
 * a small pop after a moment, without movement for visitors who turned it off.
 */
export function WhatsAppButton({ message, label }: WhatsAppButtonProps) {
  const href = whatsappLink(message)
  if (!href) return null
  const text = label || words.chat
  return (
    <a
      href={href}
      target='_blank'
      rel='noopener noreferrer'
      aria-label={text}
      className='wa-pop fixed right-4 bottom-4 z-30 inline-flex items-center gap-2 rounded-full bg-primary p-4 text-primary-foreground shadow-lg transition-transform hover:scale-105 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary md:right-6 md:bottom-6 md:px-5'
    >
      <MessageCircle className='size-6' />
      <span className='hidden text-sm font-medium md:inline'>{text}</span>
    </a>
  )
}

export default WhatsAppButton
