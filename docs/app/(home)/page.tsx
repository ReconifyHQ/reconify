import Link from 'next/link';

export default function HomePage() {
  return (
    <main className="mx-auto flex w-full max-w-3xl flex-1 flex-col justify-center px-6 py-16">
      <p className="mb-3 text-sm font-medium text-fd-muted-foreground">Reconify Documentation</p>
      <h1 className="mb-4 text-4xl font-semibold tracking-normal">
        Reconciliation docs for finance and operations workflows.
      </h1>
      <p className="mb-8 max-w-2xl text-lg text-fd-muted-foreground">
        Learn how to configure CSV sources, run the CLI, and understand the matching engine behind Reconify.
      </p>
      <div>
        <Link
          href="/docs"
          className="inline-flex h-10 items-center rounded-md bg-fd-primary px-4 text-sm font-medium text-fd-primary-foreground"
        >
          Open docs
        </Link>
      </div>
    </main>
  );
}
