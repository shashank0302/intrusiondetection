import type { Metadata } from 'next'
import './globals.css'

export const metadata: Metadata = {
  title: 'IDS Dashboard',
  description: 'Real-time Intrusion Detection System Monitor',
}

export default function RootLayout({
  children,
}: {
  children: React.ReactNode
}) {
  return (
    <html lang="en">
      <body className="animated-bg min-h-screen">{children}</body>
    </html>
  )
}
