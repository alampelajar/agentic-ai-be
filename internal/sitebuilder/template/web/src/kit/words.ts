import { site } from '../site'

// The kit's own words, in the site's language.
const all = {
  id: {
    days: 'hari',
    hours: 'jam',
    minutes: 'menit',
    today: 'Hari ini',
    past: 'Sudah berlangsung',
    calendar: 'Simpan ke kalender',
    map: 'Lihat petunjuk arah',
    guestbookEmpty: 'Belum ada ucapan. Jadilah yang pertama.',
    guestbookFailed: 'Ucapan belum bisa ditampilkan.',
    guestbookLoading: 'Memuat ucapan...',
    guest: 'Tamu',
  },
  en: {
    days: 'days',
    hours: 'hours',
    minutes: 'minutes',
    today: 'Today',
    past: 'Already happened',
    calendar: 'Add to calendar',
    map: 'Get directions',
    guestbookEmpty: 'No messages yet. Be the first.',
    guestbookFailed: 'Messages cannot be shown right now.',
    guestbookLoading: 'Loading messages...',
    guest: 'Guest',
  },
}

export const words = site.language === 'en' ? all.en : all.id
