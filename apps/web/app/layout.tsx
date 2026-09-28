import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "GITOWN — Your code. Your corner of the internet.",
  description:
    "An independent home for your repositories, ideas, and next great project.",
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
