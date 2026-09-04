import { useTranslation } from 'react-i18next'

export type OriginalPriceEntry = { key: string; label?: string; price: string }

export function OriginalPriceRow({
  entries,
  tokenUnitLabel,
}: {
  entries: OriginalPriceEntry[]
  tokenUnitLabel: string
}) {
  const { t } = useTranslation()
  if (entries.length === 0) return null
  return (
    <div className='text-muted-foreground/70 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[10px]'>
      <span>{t('Original price')}:</span>
      {entries.map((entry) => (
        <span key={entry.key} className='whitespace-nowrap'>
          {entry.label && `${entry.label} `}
          <span className='font-mono tabular-nums line-through'>
            {entry.price}/{tokenUnitLabel}
          </span>
        </span>
      ))}
    </div>
  )
}
