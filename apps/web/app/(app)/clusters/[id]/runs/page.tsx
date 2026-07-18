'use client';
// The Runs page has been removed — all execution history is in Transactions.
import { useEffect } from 'react';
import { useParams, useRouter } from 'next/navigation';

export default function RunsRedirect() {
  const params = useParams<{ id: string }>();
  const router = useRouter();
  useEffect(() => { router.replace(`/clusters/${params.id}/transactions`); }, [params.id, router]);
  return null;
}
