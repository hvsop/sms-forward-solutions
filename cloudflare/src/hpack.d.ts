declare module 'hpack.js' {
  const hpack: {
    decompressor: { create(options: { table: { size: number } }): {
      write(value: Uint8Array): void; execute(): void;
      read(): { name: string; value: string } | null;
      on(event: 'error', listener: (error: Error) => void): void;
    } };
  };
  export default hpack;
}
