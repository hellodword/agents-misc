export function EntryTime({ value, locale }: { value: Date; locale: string }) {
  return (
    <time
      className="entry-time"
      dateTime={value.toISOString()}
      title={new Intl.DateTimeFormat(locale, {
        dateStyle: "medium",
        timeStyle: "medium",
      }).format(value)}
    >
      {new Intl.DateTimeFormat(locale, {
        hour: "2-digit",
        minute: "2-digit",
        hourCycle: "h23",
      }).format(value)}
    </time>
  );
}
