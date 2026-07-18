'use client';
// The actions catalog has been removed — all execution is now via MCP transactions.
import { useEffect } from 'react';
import { useParams, useRouter } from 'next/navigation';

export default function ActionsRedirect() {
  const params = useParams<{ id: string }>();
  const router = useRouter();
  useEffect(() => { router.replace(`/clusters/${params.id}/transactions`); }, [params.id, router]);
  return null;
}
