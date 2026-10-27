import { useState } from "react";
import { ApiError, fieldMessage, formMessage } from "@/api/client";
import type { FieldError } from "@/api/schemas";

/**
 * useProblemErrors keeps a form's field errors and its form-level message.
 * capture takes them from a 422, whose field errors go to their fields
 * where shown says the form has a slot for one, and to the form-level
 * message otherwise.
 */
export function useProblemErrors(shown: (field: string) => boolean) {
  const [errors, setErrors] = useState<FieldError[]>([]);
  const [formError, setFormError] = useState("");
  return {
    errors,
    formError,
    /** show replaces the errors, with no form-level message. */
    show: (fieldErrors: FieldError[]) => {
      setErrors(fieldErrors);
      setFormError("");
    },
    /** fail shows message at the form level, keeping the field errors. */
    fail: setFormError,
    /** capture shows error if it is a 422, and reports whether it was. */
    capture: (error: unknown): boolean => {
      if (!(error instanceof ApiError && error.status === 422)) return false;
      setErrors(error.fieldErrors);
      setFormError(formMessage(error, shown));
      return true;
    },
    message: (field: string) => fieldMessage(errors, field),
  };
}
